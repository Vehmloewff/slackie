use crate::config::{is_session, token, Config};
use crate::error::{error_report, AppError, Result};
use error_stack::{Report, ResultExt};
use reqwest::{Client, Method};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::sync::{Arc, OnceLock};
use std::time::{Duration, Instant};
use tokio::sync::{Mutex as AsyncMutex, Semaphore, SemaphorePermit};
const API: &str = "https://slack.com/api/";
/// Minimum delay between the start of two Slack API requests.
const THROTTLE_SPACING: Duration = Duration::from_millis(200);
/// Maximum number of Slack API requests in flight at once.
const THROTTLE_IN_FLIGHT: usize = 4;

/// Shared rate limiter: bounds request start rate and concurrency so that
/// backfill, live sync, reads, and sends never hammer Slack at once.
pub struct Throttle {
    permits: Semaphore,
    spacing: Duration,
    next_start: AsyncMutex<Instant>,
}

pub struct ThrottleSlot<'a> {
    _permit: SemaphorePermit<'a>,
}

impl Throttle {
    fn new() -> Self {
        Self {
            permits: Semaphore::new(THROTTLE_IN_FLIGHT),
            spacing: THROTTLE_SPACING,
            next_start: AsyncMutex::new(Instant::now()),
        }
    }
    /// Wait until a request slot is allowed, holding it for the request's lifetime.
    async fn slot(&self) -> ThrottleSlot<'_> {
        let permit = self
            .permits
            .acquire()
            .await
            .expect("throttle semaphore closed");
        let wake = {
            let mut next = self.next_start.lock().await;
            let now = Instant::now();
            let at = if *next > now { *next } else { now };
            *next = at + self.spacing;
            at
        };
        let now = Instant::now();
        if wake > now {
            tokio::time::sleep(wake - now).await;
        }
        ThrottleSlot { _permit: permit }
    }
}

fn global_throttle() -> Arc<Throttle> {
    static THROTTLE: OnceLock<Arc<Throttle>> = OnceLock::new();
    THROTTLE.get_or_init(|| Arc::new(Throttle::new())).clone()
}

#[derive(Clone)]
pub struct Slack {
    pub http: Client,
    pub token: String,
    pub cookie: String,
    pub throttle: Arc<Throttle>,
}
impl Slack {
    pub fn new(token: &str) -> Result<Self> {
        Self::with_cookie(token, "")
    }
    pub fn with_cookie(token: &str, cookie: &str) -> Result<Self> {
        Ok(Self {
            http: Client::builder()
                .timeout(Duration::from_secs(30))
                .user_agent("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36 slackie")
                .build()
                .map_err(Report::new).change_context(AppError::Context("build HTTP client"))?,
            token: token.into(),
            cookie: cookie.into(),
            throttle: global_throttle(),
        })
    }
    pub fn for_config(c: &Config) -> Result<Self> {
        if is_session(c) {
            Self::with_cookie(token(c), &c.session_cookie)
        } else {
            Self::new(token(c))
        }
    }
    pub fn auth(&self, r: reqwest::RequestBuilder) -> reqwest::RequestBuilder {
        let r = r.bearer_auth(&self.token);
        if self.cookie.is_empty() {
            r
        } else {
            r.header(reqwest::header::COOKIE, format!("d={}", self.cookie))
        }
    }
    pub async fn call<T: for<'de> Deserialize<'de>>(
        &self,
        method: &str,
        form: &[(&str, String)],
    ) -> Result<T> {
        let r = self
            .auth(self.http.request(Method::POST, format!("{API}{method}")))
            .form(form);
        let response = self.send(r).await?;
        let status = response.status();
        let v: Value = response
            .json()
            .await
            .map_err(Report::new)
            .change_context(AppError::Context("decode Slack API response"))?;
        if !status.is_success() {
            return Err(error_report(format!("{method} failed: HTTP {status}: {v}")));
        }
        if v.get("ok").and_then(Value::as_bool) == Some(false) {
            return Err(error_report(format!(
                "{method} failed: {}",
                v["error"].as_str().unwrap_or("unknown_error")
            )));
        }
        serde_json::from_value(v)
            .map_err(Report::new)
            .change_context(AppError::Context("decode Slack API result"))
    }
    pub async fn get<T: for<'de> Deserialize<'de>>(
        &self,
        method: &str,
        params: &[(&str, String)],
    ) -> Result<T> {
        let r = self
            .auth(self.http.get(format!("{API}{method}")))
            .query(params);
        let response = self.send(r).await?;
        let status = response.status();
        let v: Value = response
            .json()
            .await
            .map_err(Report::new)
            .change_context(AppError::Context("decode Slack API response"))?;
        if !status.is_success() || v.get("ok").and_then(Value::as_bool) == Some(false) {
            return Err(error_report(format!(
                "{method} failed: {}",
                v["error"].as_str().unwrap_or("HTTP error")
            )));
        }
        serde_json::from_value(v)
            .map_err(Report::new)
            .change_context(AppError::Context("decode Slack API result"))
    }
    /// Send a Slack API (or authenticated CDN) request through the shared
    /// throttle, retrying rate limits with backoff.
    pub async fn send(&self, r: reqwest::RequestBuilder) -> Result<reqwest::Response> {
        for attempt in 0..6u32 {
            let req = r
                .try_clone()
                .ok_or_else(|| error_report("request body not repeatable"))?
                .build()
                .map_err(Report::new)
                .change_context(AppError::Context("build Slack API request"))?;
            let method = req.method().to_string();
            let mut logged_url = req.url().clone();
            if !logged_url.path().starts_with("/api/") {
                logged_url.set_query(None);
                logged_url.set_fragment(None);
            }
            let url = logged_url.to_string();
            log::debug!("Slack request: {method} {url}");
            let slot = self.throttle.slot().await;
            let started = Instant::now();
            let response = r
                .try_clone()
                .ok_or_else(|| error_report("request body not repeatable"))?
                .send()
                .await
                .map_err(Report::new)
                .change_context(AppError::Context("send Slack API request"))?;
            log::debug!(
                "Slack response: {method} {url} -> {} ({:?})",
                response.status(),
                started.elapsed()
            );
            if response.status() == reqwest::StatusCode::TOO_MANY_REQUESTS {
                let wait = response
                    .headers()
                    .get(reqwest::header::RETRY_AFTER)
                    .and_then(|v| v.to_str().ok())
                    .and_then(|v| v.parse::<u64>().ok())
                    .unwrap_or(u64::from(2 * (attempt + 1)));
                log::info!("rate limited, retrying in {wait}s (attempt {attempt})");
                drop(slot);
                tokio::time::sleep(Duration::from_secs(wait.min(30))).await;
                continue;
            }
            return Ok(response);
        }
        Err(error_report("Slack API rate limited: too many retries"))
    }
}
#[derive(Debug, Deserialize)]
pub struct Auth {
    #[serde(default)]
    pub user_id: String,
    #[serde(default)]
    pub team_id: String,
    #[serde(default)]
    pub team: String,
}
#[derive(Debug, Clone, Default, Deserialize, Serialize)]
pub struct Conv {
    #[serde(default)]
    pub id: String,
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub user: String,
    #[serde(default)]
    pub is_im: bool,
    #[serde(default)]
    pub is_mpim: bool,
    #[serde(default)]
    pub is_group: bool,
    #[serde(default, skip_serializing)]
    pub latest: Msg,
}
#[derive(Debug, Clone, Default, Deserialize)]
pub struct Msg {
    #[serde(default)]
    pub user: String,
    #[serde(default)]
    pub text: String,
    #[serde(default)]
    pub ts: String,
    #[serde(default)]
    pub thread_ts: String,
    #[serde(default)]
    pub reply_count: u64,
    #[serde(default)]
    pub subtype: String,
    #[serde(default)]
    pub username: String,
    #[serde(default)]
    pub bot_id: String,
    #[serde(default)]
    pub files: Vec<SlackFile>,
    #[serde(default)]
    pub attachments: Vec<SlackAttachment>,
}
#[derive(Debug, Clone, Default, Deserialize, Serialize)]
pub struct SlackFile {
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub title: String,
    #[serde(default)]
    pub is_external: bool,
    #[serde(default)]
    pub url_private: String,
    #[serde(default)]
    pub url_private_download: String,
}
#[derive(Debug, Clone, Default, Deserialize, Serialize)]
pub struct SlackAttachment {
    #[serde(default)]
    pub title: String,
    #[serde(default)]
    pub image_url: String,
    #[serde(default)]
    pub thumb_url: String,
}
#[derive(Deserialize)]
pub struct List<T> {
    #[serde(default)]
    pub channels: Vec<T>,
    #[serde(default)]
    pub response_metadata: Meta,
}
#[derive(Default, Deserialize)]
pub struct Meta {
    #[serde(default)]
    pub next_cursor: String,
}
#[derive(Deserialize)]
pub struct History {
    #[serde(default)]
    pub messages: Vec<Msg>,
    #[serde(default)]
    pub response_metadata: Meta,
}
#[derive(Deserialize)]
pub struct Users {
    #[serde(default)]
    pub members: Vec<User>,
    #[serde(default)]
    pub response_metadata: Meta,
}
#[derive(Debug, Clone, Default, Deserialize)]
pub struct User {
    pub id: String,
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub real_name: String,
    #[serde(default)]
    pub profile: Profile,
    #[serde(default)]
    pub deleted: bool,
}
#[derive(Debug, Clone, Default, Deserialize)]
pub struct Profile {
    #[serde(default)]
    pub display_name: String,
    #[serde(default)]
    pub display_name_normalized: String,
    #[serde(default)]
    pub real_name: String,
}

#[cfg(test)]
mod tests {
    use super::Throttle;
    use std::time::{Duration, Instant};

    #[tokio::test]
    async fn throttle_spaces_out_request_starts() {
        let throttle = Throttle::new();
        let start = Instant::now();
        let mut marks = Vec::new();
        for _ in 0..4 {
            let slot = throttle.slot().await;
            marks.push(start.elapsed());
            drop(slot);
        }
        assert!(marks[0] < Duration::from_millis(100), "{marks:?}");
        // Every later request start is spaced by at least THROTTLE_SPACING.
        assert!(marks[3] >= Duration::from_millis(600), "{marks:?}");
    }
}

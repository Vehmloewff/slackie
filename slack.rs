use crate::config::{is_session, token, Config};
use crate::error::{error_report, AppError, Result};
use error_stack::{Report, ResultExt};
use reqwest::{Client, Method};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::time::Duration;
const API: &str = "https://slack.com/api/";
#[derive(Clone)]
pub(crate) struct Slack {
    pub(crate) http: Client,
    pub(crate) token: String,
    pub(crate) cookie: String,
}
impl Slack {
    pub(crate) fn new(token: &str) -> Result<Self> {
        Self::with_cookie(token, "")
    }
    pub(crate) fn with_cookie(token: &str, cookie: &str) -> Result<Self> {
        Ok(Self {
            http: Client::builder()
                .timeout(Duration::from_secs(30))
                .user_agent("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36 slackie")
                .build()
                .map_err(Report::new).change_context(AppError::Context("build HTTP client"))?,
            token: token.into(),
            cookie: cookie.into(),
        })
    }
    pub(crate) fn for_config(c: &Config) -> Result<Self> {
        if is_session(c) {
            Self::with_cookie(token(c), &c.session_cookie)
        } else {
            Self::new(token(c))
        }
    }
    pub(crate) fn auth(&self, r: reqwest::RequestBuilder) -> reqwest::RequestBuilder {
        let r = r.bearer_auth(&self.token);
        if self.cookie.is_empty() {
            r
        } else {
            r.header(reqwest::header::COOKIE, format!("d={}", self.cookie))
        }
    }
    pub(crate) async fn call<T: for<'de> Deserialize<'de>>(
        &self,
        method: &str,
        form: &[(&str, String)],
    ) -> Result<T> {
        let r = self
            .auth(self.http.request(Method::POST, format!("{API}{method}")))
            .form(form);
        let response = send_with_retry(r).await?;
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
    pub(crate) async fn get<T: for<'de> Deserialize<'de>>(
        &self,
        method: &str,
        params: &[(&str, String)],
    ) -> Result<T> {
        let r = self
            .auth(self.http.get(format!("{API}{method}")))
            .query(params);
        let response = send_with_retry(r).await?;
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
}
pub(crate) async fn send_with_retry(r: reqwest::RequestBuilder) -> Result<reqwest::Response> {
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
        let started = std::time::Instant::now();
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
            eprintln!("slackie: rate limited, retrying in {wait}s (attempt {attempt})");
            tokio::time::sleep(Duration::from_secs(wait.min(30))).await;
            continue;
        }
        return Ok(response);
    }
    Err(error_report("Slack API rate limited: too many retries"))
}
#[derive(Debug, Deserialize)]
pub(crate) struct Auth {
    #[serde(default)]
    pub(crate) user_id: String,
    #[serde(default)]
    pub(crate) team_id: String,
    #[serde(default)]
    pub(crate) team: String,
}
#[derive(Debug, Clone, Default, Deserialize, Serialize)]
pub(crate) struct Conv {
    #[serde(default)]
    pub(crate) id: String,
    #[serde(default)]
    pub(crate) name: String,
    #[serde(default)]
    pub(crate) user: String,
    #[serde(default)]
    pub(crate) is_im: bool,
    #[serde(default)]
    pub(crate) is_mpim: bool,
    #[serde(default)]
    pub(crate) is_group: bool,
    #[serde(default, skip_serializing)]
    pub(crate) latest: Msg,
}
#[derive(Debug, Clone, Default, Deserialize)]
pub(crate) struct Msg {
    #[serde(default)]
    pub(crate) user: String,
    #[serde(default)]
    pub(crate) text: String,
    #[serde(default)]
    pub(crate) ts: String,
    #[serde(default)]
    pub(crate) thread_ts: String,
    #[serde(default)]
    pub(crate) subtype: String,
    #[serde(default)]
    pub(crate) username: String,
    #[serde(default)]
    pub(crate) bot_id: String,
    #[serde(default)]
    pub(crate) files: Vec<SlackFile>,
    #[serde(default)]
    pub(crate) attachments: Vec<SlackAttachment>,
}
#[derive(Debug, Clone, Default, Deserialize)]
pub(crate) struct SlackFile {
    #[serde(default)]
    pub(crate) name: String,
    #[serde(default)]
    pub(crate) title: String,
    #[serde(default)]
    pub(crate) is_external: bool,
    #[serde(default)]
    pub(crate) url_private: String,
    #[serde(default)]
    pub(crate) url_private_download: String,
}
#[derive(Debug, Clone, Default, Deserialize)]
pub(crate) struct SlackAttachment {
    #[serde(default)]
    pub(crate) title: String,
    #[serde(default)]
    pub(crate) image_url: String,
    #[serde(default)]
    pub(crate) thumb_url: String,
}
#[derive(Deserialize)]
pub(crate) struct List<T> {
    #[serde(default)]
    pub(crate) channels: Vec<T>,
    #[serde(default)]
    pub(crate) response_metadata: Meta,
}
#[derive(Default, Deserialize)]
pub(crate) struct Meta {
    #[serde(default)]
    pub(crate) next_cursor: String,
}
#[derive(Deserialize)]
pub(crate) struct History {
    #[serde(default)]
    pub(crate) messages: Vec<Msg>,
    #[serde(default)]
    pub(crate) response_metadata: Meta,
}
#[derive(Deserialize)]
pub(crate) struct Users {
    #[serde(default)]
    pub(crate) members: Vec<User>,
    #[serde(default)]
    pub(crate) response_metadata: Meta,
}
#[derive(Deserialize)]
pub(crate) struct User {
    pub(crate) id: String,
    #[serde(default)]
    pub(crate) name: String,
    #[serde(default)]
    pub(crate) real_name: String,
    #[serde(default)]
    pub(crate) profile: Profile,
    #[serde(default)]
    pub(crate) deleted: bool,
}
#[derive(Default, Deserialize)]
pub(crate) struct Profile {
    #[serde(default)]
    pub(crate) display_name: String,
    #[serde(default)]
    pub(crate) display_name_normalized: String,
    #[serde(default)]
    pub(crate) real_name: String,
}

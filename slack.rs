use err::{Err, Result, ResultExt};
use reqwest::{Client, Method};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::time::Duration;
const API: &str = "https://slack.com/api/";
#[derive(Clone)]
pub(crate) struct Slack {
    pub(crate) http: Client,
    pub(crate) token: String,
}
impl Slack {
    pub(crate) fn new(token: &str) -> Result<Self> {
        Ok(Self {
            http: Client::builder()
                .timeout(Duration::from_secs(30))
                .build()
                .wrap("build HTTP client")?,
            token: token.into(),
        })
    }
    pub(crate) async fn call<T: for<'de> Deserialize<'de>>(
        &self,
        method: &str,
        form: &[(&str, String)],
    ) -> Result<T> {
        let r = self
            .http
            .request(Method::POST, format!("{API}{method}"))
            .bearer_auth(&self.token)
            .form(form)
            .send()
            .await
            .wrap("send Slack API request")?;
        let status = r.status();
        let v: Value = r.json().await.wrap("decode Slack API response")?;
        if !status.is_success() {
            return Err(Err::from_error(format!(
                "{method} failed: HTTP {status}: {v}"
            )));
        }
        if v.get("ok").and_then(Value::as_bool) == Some(false) {
            return Err(Err::from_error(format!(
                "{method} failed: {}",
                v["error"].as_str().unwrap_or("unknown_error")
            )));
        }
        serde_json::from_value(v).wrap("decode Slack API result")
    }
    pub(crate) async fn get<T: for<'de> Deserialize<'de>>(
        &self,
        method: &str,
        params: &[(&str, String)],
    ) -> Result<T> {
        let r = self
            .http
            .get(format!("{API}{method}"))
            .bearer_auth(&self.token)
            .query(params)
            .send()
            .await
            .wrap("send Slack API request")?;
        let status = r.status();
        let v: Value = r.json().await.wrap("decode Slack API response")?;
        if !status.is_success() || v.get("ok").and_then(Value::as_bool) == Some(false) {
            return Err(Err::from_error(format!(
                "{method} failed: {}",
                v["error"].as_str().unwrap_or("HTTP error")
            )));
        }
        serde_json::from_value(v).wrap("decode Slack API result")
    }
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

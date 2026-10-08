use crate::{config::Config, slack::Slack};
use err::{Err, Result, ResultExt};
use serde::Deserialize;
use serde_json::{json, Value};
use std::collections::HashMap;
use tokio_tungstenite::{connect_async, tungstenite::Message};
pub(crate) async fn socket_mode_wait(
    c: &Config,
    names: &HashMap<String, String>,
) -> Result<String> {
    use futures_util::{SinkExt, StreamExt};
    #[derive(Deserialize)]
    struct Open {
        url: String,
    }
    let socket = Slack::new(&c.app_token)?;
    let opened: Open = socket
        .call("apps.connections.open", &[])
        .await
        .wrap("open Socket Mode connection")?;
    let (mut ws, _) = connect_async(&opened.url)
        .await
        .wrap("connect Socket Mode websocket")?;
    while let Some(frame) = ws.next().await {
        let frame = frame.wrap("read Socket Mode websocket")?;
        let Message::Text(text) = frame else { continue };
        let envelope: Value = serde_json::from_str(&text).wrap("decode Socket Mode envelope")?;
        if let Some(id) = envelope["envelope_id"].as_str() {
            ws.send(Message::Text(json!({"envelope_id":id}).to_string().into()))
                .await
                .wrap("acknowledge Socket Mode envelope")?;
        }
        match envelope["type"].as_str().unwrap_or("") {
            "hello" => continue,
            "disconnect" => {
                return Err(Err::from_error(format!(
                    "Socket Mode disconnected: {}",
                    envelope["reason"].as_str().unwrap_or("unknown_reason")
                )))
            }
            "events_api" => {
                let event = &envelope["payload"]["event"];
                let channel = event["channel"].as_str().unwrap_or("");
                let ts = event["ts"].as_str().unwrap_or("");
                let user = event["user"].as_str().unwrap_or("");
                let text = event["text"].as_str().unwrap_or("");
                let ctype = event["channel_type"].as_str().unwrap_or("");
                if channel.is_empty()
                    || ts.is_empty()
                    || user == c.bot_user_id
                    || !event["bot_id"].as_str().unwrap_or("").is_empty()
                {
                    continue;
                }
                if (ctype == "im" && !c.read_dms)
                    || (ctype == "mpim" && !c.read_message_groups)
                    || (ctype == "group" && !c.read_private_channels)
                {
                    continue;
                }
                let mention = event["type"] == "app_mention"
                    || (!c.bot_user_id.is_empty()
                        && text.contains(&format!("<@{}>", c.bot_user_id)));
                if c.read_app_mentions_only && !matches!(ctype, "im" | "mpim") && !mention {
                    continue;
                }
                let name = names.get(user).cloned().unwrap_or_else(|| user.to_owned());
                let title = if ctype == "im" {
                    format!("@{name}")
                } else {
                    format!("#{channel}")
                };
                println!("[{title}] {ts}: {}", text.replace('\n', " "));
                let thread_ts = event["thread_ts"]
                    .as_str()
                    .filter(|value| !value.is_empty())
                    .unwrap_or(ts);
                println!("  threading target: {channel}:{thread_ts}");
                return Ok(ts.to_owned());
            }
            _ => continue,
        }
    }
    Err(Err::new(
        "Socket Mode websocket closed before receiving a message",
    ))
}

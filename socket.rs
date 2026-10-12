use crate::config::{self, Config};
use crate::error::{error_report, AppError, Result};
use crate::slack::Slack;
use error_stack::{Report, ResultExt};
use futures_util::{SinkExt, StreamExt};
use serde::Deserialize;
use serde_json::{json, Value};
use std::fs;
use std::time::Duration;
use tokio::sync::mpsc::Sender;
use tokio_tungstenite::{connect_async, tungstenite::Message};

/// A message event pulled off the live event stream, destined for the store.
#[derive(Debug, Clone)]
pub struct Event {
    pub channel: String,
    pub ts: String,
    pub user: String,
    pub text: String,
    pub subtype: String,
    pub thread_ts: String,
    pub bot_id: String,
    pub channel_type: String,
}

/// Connect to Slack's live event stream and forward message events until the
/// connection drops. Chooses Socket Mode when an app token exists, otherwise
/// RTM over the impersonation session.
pub async fn listen(c: &Config, tx: &Sender<Event>) -> Result<()> {
    if !c.app_token.is_empty() {
        socket_mode(c, tx).await
    } else if !c.session_token.is_empty() {
        rtm(c, tx).await
    } else {
        idle_until_config_changes().await
    }
}

/// No event stream is possible without an app-level or session token; idle
/// until the config changes so the daemon can pick up fresh credentials.
async fn idle_until_config_changes() -> Result<()> {
    log::info!("no app-level or session token: no live event stream, relying on periodic sync");
    let path = config::config_path()?;
    let mut seen = stamp(&path);
    loop {
        tokio::time::sleep(Duration::from_secs(5)).await;
        let now = stamp(&path);
        if now != seen && now.is_some() {
            return Ok(());
        }
        seen = now;
    }
}

fn stamp(path: &std::path::Path) -> Option<std::time::SystemTime> {
    fs::metadata(path).and_then(|m| m.modified()).ok()
}

async fn rtm(c: &Config, tx: &Sender<Event>) -> Result<()> {
    #[derive(Deserialize)]
    struct Connect {
        url: String,
    }
    let slack = Slack::with_cookie(&c.session_token, &c.session_cookie)?;
    let opened: Connect = slack
        .call("rtm.connect", &[])
        .await
        .change_context(AppError::Context("open RTM connection"))?;
    let (mut ws, _) = connect_async(&opened.url)
        .await
        .map_err(Report::new)
        .change_context(AppError::Context("connect RTM websocket"))?;
    while let Some(frame) = ws.next().await {
        let frame = frame
            .map_err(Report::new)
            .change_context(AppError::Context("read RTM websocket"))?;
        let Message::Text(text) = frame else { continue };
        let event: Value = serde_json::from_str(&text)
            .map_err(Report::new)
            .change_context(AppError::Context("decode RTM event"))?;
        if let Some(ev) = parse_event(&event) {
            tx.send(ev)
                .await
                .map_err(|_| error_report("event sink closed"))?;
        }
    }
    Err(error_report("RTM websocket closed"))
}

async fn socket_mode(c: &Config, tx: &Sender<Event>) -> Result<()> {
    #[derive(Deserialize)]
    struct Open {
        url: String,
    }
    let socket = Slack::new(&c.app_token)?;
    let opened: Open = socket
        .call("apps.connections.open", &[])
        .await
        .change_context(AppError::Context("open Socket Mode connection"))?;
    let (mut ws, _) = connect_async(&opened.url)
        .await
        .map_err(Report::new)
        .change_context(AppError::Context("connect Socket Mode websocket"))?;
    while let Some(frame) = ws.next().await {
        let frame = frame
            .map_err(Report::new)
            .change_context(AppError::Context("read Socket Mode websocket"))?;
        let Message::Text(text) = frame else { continue };
        let envelope: Value = serde_json::from_str(&text)
            .map_err(Report::new)
            .change_context(AppError::Context("decode Socket Mode envelope"))?;
        if let Some(id) = envelope["envelope_id"].as_str() {
            ws.send(Message::Text(json!({"envelope_id":id}).to_string().into()))
                .await
                .map_err(Report::new)
                .change_context(AppError::Context("acknowledge Socket Mode envelope"))?;
        }
        match envelope["type"].as_str().unwrap_or("") {
            "hello" => continue,
            "disconnect" => {
                return Err(error_report(format!(
                    "Socket Mode disconnected: {}",
                    envelope["reason"].as_str().unwrap_or("unknown_reason")
                )))
            }
            "events_api" => {
                if let Some(ev) = parse_event(&envelope["payload"]["event"]) {
                    tx.send(ev)
                        .await
                        .map_err(|_| error_report("event sink closed"))?;
                }
            }
            _ => continue,
        }
    }
    Err(error_report("Socket Mode websocket closed"))
}

fn parse_event(event: &Value) -> Option<Event> {
    let kind = event["type"].as_str()?;
    if kind != "message" && kind != "app_mention" {
        return None;
    }
    let channel = event["channel"].as_str()?;
    let ts = event["ts"].as_str()?;
    if channel.is_empty() || ts.is_empty() {
        return None;
    }
    Some(Event {
        channel: channel.to_owned(),
        ts: ts.to_owned(),
        user: event["user"].as_str().unwrap_or("").to_owned(),
        text: event["text"].as_str().unwrap_or("").to_owned(),
        subtype: event["subtype"].as_str().unwrap_or("").to_owned(),
        thread_ts: event["thread_ts"].as_str().unwrap_or("").to_owned(),
        bot_id: event["bot_id"].as_str().unwrap_or("").to_owned(),
        channel_type: event["channel_type"].as_str().unwrap_or("").to_owned(),
    })
}

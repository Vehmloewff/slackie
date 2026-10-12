use crate::config::{load_config, Config};
use crate::db::Db;
use crate::error::{error_report, AppError, Result};
use crate::slack::{Conv, Msg, Slack};
use error_stack::{Report, ResultExt};
use serde::Deserialize;
use std::collections::HashMap;
use std::fmt::Write;
use std::{env, fs, path::PathBuf};
use tokio::sync::watch;

/// Print unread messages from the local store, advancing the read cursor.
/// With `wait`, block until the daemon records a message that matches the
/// read preferences, then print it.
pub async fn read(db: &Db, wait: bool, rev: &mut watch::Receiver<u64>) -> Result<String> {
    let cfg = load_config()?;
    ensure_last_seen(db, &cfg).await?;
    let slack = Slack::for_config(&cfg)?;
    loop {
        let after = db.messages_after(&last_seen(db).await?).await?;
        let newest = after.last().map(|(_, m)| m.ts.clone());
        let found = matching(&cfg, after);
        if found.is_empty() {
            // Advance past messages the read preferences exclude, then either
            // return or wait for the daemon to record something new.
            if let Some(ts) = newest {
                db.state_set("last_seen", &ts).await?;
            }
            if !wait {
                return Ok(String::new());
            }
            rev.changed()
                .await
                .map_err(|_| error_report("daemon is shutting down"))?;
            continue;
        }
        let out = render(db, &slack, &found).await?;
        if let Some(ts) = newest {
            db.state_set("last_seen", &ts).await?;
        }
        return Ok(out);
    }
}

/// Seed the read cursor the first time the daemon runs (setup time, or now).
pub async fn ensure_last_seen(db: &Db, cfg: &Config) -> Result<()> {
    if db
        .state_get("last_seen")
        .await?
        .unwrap_or_default()
        .is_empty()
    {
        let seed = if cfg.last_seen.is_empty() {
            now_ts()
        } else {
            cfg.last_seen.clone()
        };
        db.state_set("last_seen", &seed).await?;
    }
    Ok(())
}

async fn last_seen(db: &Db) -> Result<String> {
    Ok(db.state_get("last_seen").await?.unwrap_or_default())
}

fn now_ts() -> String {
    let d = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap_or_default();
    format!("{}.{:06}", d.as_secs(), d.subsec_micros())
}

fn matching(cfg: &Config, found: Vec<(Conv, Msg)>) -> Vec<(Conv, Msg)> {
    found
        .into_iter()
        .filter(|(cv, m)| {
            if !m.subtype.is_empty() || m.user == cfg.bot_user_id || !m.bot_id.is_empty() {
                return false;
            }
            if cv.is_im && !cfg.read_dms {
                return false;
            }
            if cv.is_mpim && !cfg.read_message_groups {
                return false;
            }
            if cv.is_group && !cfg.read_private_channels {
                return false;
            }
            if cfg.read_app_mentions_only
                && !cv.is_im
                && !cv.is_mpim
                && !m.text.contains(&format!("<@{}>", cfg.bot_user_id))
            {
                return false;
            }
            true
        })
        .collect()
}

async fn render(db: &Db, slack: &Slack, found: &[(Conv, Msg)]) -> Result<String> {
    let mut names = HashMap::new();
    let mut out = String::new();
    for (cv, m) in found {
        let title = conv_title(db, slack, cv, &mut names).await?;
        let sender = if !m.user.is_empty() {
            user_name(db, slack, &m.user, &mut names).await?
        } else {
            m.username.clone()
        };
        write!(
            out,
            "[{title}] {} {}: {}",
            m.ts,
            sender,
            m.text.replace('\n', " ")
        )
        .map_err(|_| error_report("format output"))?;
        out.push('\n');
        let thread_ts = if m.thread_ts.is_empty() {
            &m.ts
        } else {
            &m.thread_ts
        };
        writeln!(out, "  threading target: {title}:{thread_ts}")
            .map_err(|_| error_report("format output"))?;
        for path in download_message_attachments(slack, m).await? {
            writeln!(out, "  {}", path.display()).map_err(|_| error_report("format output"))?;
        }
    }
    Ok(out)
}

#[derive(Deserialize)]
struct UserInfo {
    user: crate::slack::User,
}

async fn user_name(
    db: &Db,
    slack: &Slack,
    id: &str,
    names: &mut HashMap<String, String>,
) -> Result<String> {
    if let Some(name) = names.get(id) {
        return Ok(name.clone());
    }
    if id.is_empty() {
        return Ok(String::new());
    }
    let mut user = match db.user(id).await? {
        Some(u) => Some(u),
        None => {
            let info: UserInfo = slack
                .get("users.info", &[("user", id.to_owned())])
                .await
                .change_context(AppError::Context("load user for display"))?;
            db.upsert_user(&info.user).await?;
            Some(info.user)
        }
    };
    let name = match user.take() {
        Some(u) if !u.profile.display_name.is_empty() => u.profile.display_name,
        Some(u) if !u.real_name.is_empty() => u.real_name,
        Some(u) if !u.name.is_empty() => u.name,
        _ => id.to_owned(),
    };
    names.insert(id.to_owned(), name.clone());
    Ok(name)
}

async fn conv_title(
    db: &Db,
    slack: &Slack,
    cv: &Conv,
    names: &mut HashMap<String, String>,
) -> Result<String> {
    if cv.is_im {
        let dm_user = if cv.user.is_empty() {
            cv.id.clone()
        } else {
            user_name(db, slack, &cv.user, names).await?
        };
        Ok(format!("@{dm_user}"))
    } else if cv.is_mpim {
        Ok(format!("MPIM {}", cv.name))
    } else if cv.name.is_empty() {
        Ok(format!("#{}", cv.id))
    } else {
        Ok(format!("#{}", cv.name))
    }
}

async fn download_message_attachments(slack: &Slack, message: &Msg) -> Result<Vec<PathBuf>> {
    let mut urls = Vec::<(String, String)>::new();
    for file in &message.files {
        if file.is_external {
            continue;
        }
        let name = if file.name.is_empty() {
            &file.title
        } else {
            &file.name
        };
        let url = if !file.url_private_download.is_empty() {
            &file.url_private_download
        } else {
            &file.url_private
        };
        if !url.is_empty() {
            urls.push((url.clone(), name.clone()));
        }
    }
    for attachment in &message.attachments {
        for url in [&attachment.image_url, &attachment.thumb_url] {
            if !url.is_empty() {
                urls.push((url.clone(), attachment.title.clone()));
            }
        }
    }
    let mut paths = Vec::new();
    for (url, suggested) in urls {
        let response = slack
            .send(slack.auth(slack.http.get(&url)))
            .await
            .change_context(AppError::Context("download message attachment"))?;
        if !response.status().is_success() {
            return Err(error_report(format!(
                "download attachment failed: HTTP {}",
                response.status()
            )));
        }
        if response
            .headers()
            .get(reqwest::header::CONTENT_TYPE)
            .and_then(|h| h.to_str().ok())
            .unwrap_or("")
            .to_ascii_lowercase()
            .starts_with("text/html")
        {
            continue;
        }
        let name = PathBuf::from(&suggested)
            .file_name()
            .and_then(|v| v.to_str())
            .filter(|v| !v.is_empty())
            .unwrap_or("attachment")
            .to_owned();
        let safe_ts = message.ts.replace('.', "-");
        let path = env::temp_dir().join(format!("slackie-{safe_ts}-{name}"));
        let bytes = response
            .bytes()
            .await
            .map_err(Report::new)
            .change_context(AppError::Context("read attachment response"))?;
        fs::write(&path, bytes)
            .map_err(Report::new)
            .change_context(AppError::Context("write downloaded attachment"))?;
        paths.push(path);
    }
    Ok(paths)
}

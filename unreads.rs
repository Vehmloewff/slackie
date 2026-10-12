use crate::error::{error_report, AppError, Result};
use crate::socket::{rtm_wait, socket_mode_wait};
use crate::{
    config::{load_config, save_config, Config},
    slack::{Conv, History, List, Msg, Slack, User, Users},
};
use error_stack::{Report, ResultExt};
use serde::Deserialize;
use std::{collections::HashMap, env, fs, path::PathBuf};
pub(crate) async fn list_convs(s: &Slack, c: &Config) -> Result<Vec<Conv>> {
    if crate::config::is_session(c) {
        let user = if c.user_id.is_empty() {
            c.bot_user_id.clone()
        } else {
            c.user_id.clone()
        };
        let mut types = vec!["public_channel"];
        if c.read_private_channels {
            types.push("private_channel");
        }
        if c.read_dms {
            types.push("im");
        }
        if c.read_message_groups {
            types.push("mpim");
        }
        let types = types.join(",");
        let mut cursor = String::new();
        let mut all = vec![];
        loop {
            let mut p = vec![
                ("types", types.clone()),
                ("limit", "200".into()),
                ("user", user.clone()),
            ];
            if !cursor.is_empty() {
                p.push(("cursor", cursor.clone()));
            }
            let r: List<Conv> = s.get("users.conversations", &p).await?;
            all.extend(r.channels);
            cursor = r.response_metadata.next_cursor;
            if cursor.is_empty() {
                break;
            }
        }
        return Ok(all);
    }
    let mut types = vec!["public_channel"];
    if c.read_private_channels {
        types.push("private_channel")
    }
    if c.read_dms {
        types.push("im")
    }
    if c.read_message_groups {
        types.push("mpim")
    }
    let mut cursor = String::new();
    let mut all = vec![];
    loop {
        let mut p = vec![
            ("types", types.join(",")),
            ("limit", "200".into()),
            ("exclude_archived", "true".into()),
        ];
        if !cursor.is_empty() {
            p.push(("cursor", cursor.clone()));
        }
        let r: List<Conv> = s.get("conversations.list", &p).await?;
        all.extend(r.channels);
        cursor = r.response_metadata.next_cursor;
        if cursor.is_empty() {
            break;
        }
    }
    Ok(all)
}
pub(crate) async fn list_users(s: &Slack) -> Result<Vec<User>> {
    let mut users = Vec::new();
    let mut cursor = String::new();
    loop {
        let mut params = vec![("limit", "200".to_owned())];
        if !cursor.is_empty() {
            params.push(("cursor", cursor.clone()));
        }
        let page: Users = s.get("users.list", &params).await?;
        users.extend(page.members);
        cursor = page.response_metadata.next_cursor;
        if cursor.is_empty() {
            break;
        }
    }
    Ok(users)
}

pub(crate) async fn read(wait: bool) -> Result<()> {
    let mut c = load_config()?;
    let s = Slack::for_config(&c)?;
    let mut found = scan_conversations(&s, &c).await?;
    found.sort_by(|a, b| a.1.ts.cmp(&b.1.ts));
    if let Some(latest) = found.iter().map(|(_, m)| m.ts.clone()).max() {
        if latest > c.last_seen {
            c.last_seen = latest;
        }
    }
    let mut names = HashMap::new();
    for (cv, m) in &found {
        let title = conv_title(&s, cv, &mut names).await?;
        print_message(&s, &title, m, &mut names).await?;
    }
    save_config(&c)?;
    if found.is_empty() && wait {
        c.last_seen = if !c.app_token.is_empty() {
            socket_mode_wait(&c, &names).await?
        } else if crate::config::is_session(&c) {
            rtm_wait(&c, &names).await?
        } else {
            return Err(error_report(
                "slackie read --wait requires an app-level xapp- token or impersonation setup; run slackie setup",
            ));
        };
        save_config(&c)?;
    }
    Ok(())
}

#[derive(Deserialize)]
struct UserInfo {
    user: User,
}

async fn user_name(s: &Slack, id: &str, names: &mut HashMap<String, String>) -> Result<String> {
    if let Some(name) = names.get(id) {
        return Ok(name.clone());
    }
    if id.is_empty() {
        return Ok(String::new());
    }
    let info: UserInfo = s.get("users.info", &[("user", id.to_owned())]).await?;
    let user = info.user;
    let name = if !user.profile.display_name.is_empty() {
        user.profile.display_name
    } else if !user.real_name.is_empty() {
        user.real_name
    } else if !user.name.is_empty() {
        user.name
    } else {
        id.to_owned()
    };
    names.insert(id.to_owned(), name.clone());
    Ok(name)
}

async fn conv_title(s: &Slack, cv: &Conv, names: &mut HashMap<String, String>) -> Result<String> {
    if cv.is_im {
        let dm_user = if cv.user.is_empty() {
            cv.id.clone()
        } else {
            user_name(s, &cv.user, names).await?
        };
        Ok(format!("@{dm_user}"))
    } else if cv.is_mpim {
        Ok(format!("MPIM {}", cv.name))
    } else {
        Ok(format!("#{}", cv.name))
    }
}

async fn scan_conversations(s: &Slack, c: &Config) -> Result<Vec<(Conv, Msg)>> {
    let convs = list_convs(s, c).await?;
    let mut found = vec![];
    for cv in convs {
        if !cv.latest.ts.is_empty() && cv.latest.ts <= c.last_seen {
            continue;
        }
        let mut cursor = String::new();
        loop {
            let mut p = vec![
                ("channel", cv.id.clone()),
                ("limit", "100".into()),
                ("oldest", c.last_seen.clone()),
                ("inclusive", "false".into()),
            ];
            if !cursor.is_empty() {
                p.push(("cursor", cursor.clone()));
            }
            let h: History = match s.get("conversations.history", &p).await {
                Ok(v) => v,
                Err(e) => {
                    if format!("{e:?}").contains("not_in_channel") {
                        break;
                    }
                    return Err(e);
                }
            };
            for m in h.messages {
                if m.ts <= c.last_seen
                    || !m.subtype.is_empty()
                    || m.user == c.bot_user_id
                    || !m.bot_id.is_empty()
                {
                    continue;
                }
                if c.read_app_mentions_only
                    && !cv.is_im
                    && !cv.is_mpim
                    && !m.text.contains(&format!("<@{}>", c.bot_user_id))
                {
                    continue;
                }
                found.push((cv.clone(), m));
            }
            cursor = h.response_metadata.next_cursor;
            if cursor.is_empty() {
                break;
            }
        }
    }
    Ok(found)
}

async fn print_message(
    s: &Slack,
    title: &str,
    m: &Msg,
    names: &mut HashMap<String, String>,
) -> Result<()> {
    let sender = if !m.user.is_empty() {
        user_name(s, &m.user, names).await?
    } else {
        m.username.clone()
    };
    println!(
        "[{}] {} {}: {}",
        title,
        m.ts,
        sender,
        m.text.replace('\n', " ")
    );
    let thread_ts = if m.thread_ts.is_empty() {
        &m.ts
    } else {
        &m.thread_ts
    };
    println!("  threading target: {}:{}", title, thread_ts);
    for path in download_message_attachments(s, m).await? {
        println!("  {}", path.display());
    }
    Ok(())
}

async fn download_message_attachments(s: &Slack, message: &Msg) -> Result<Vec<PathBuf>> {
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
        let response = crate::slack::send_with_retry(s.auth(s.http.get(&url)))
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

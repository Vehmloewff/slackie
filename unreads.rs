use crate::socket::socket_mode_wait;
use crate::{
    config::{load_config, save_config, token, Config},
    slack::{Conv, History, List, Msg, Slack, User, Users},
};
use err::{Err, Result, ResultExt};
use std::{collections::HashMap, env, fs, path::PathBuf};
pub(crate) async fn list_convs(s: &Slack, c: &Config) -> Result<Vec<Conv>> {
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
    let s = Slack::new(token(&c))?;
    let users = list_users(&s).await?;
    let names: HashMap<String, String> = users
        .into_iter()
        .map(|u| {
            (
                u.id,
                if !u.profile.display_name.is_empty() {
                    u.profile.display_name
                } else if !u.real_name.is_empty() {
                    u.real_name
                } else {
                    u.name
                },
            )
        })
        .collect();
    loop {
        let convs = list_convs(&s, &c).await?;
        let mut found = vec![];
        let mut scanned_latest = c.last_seen.clone();
        for cv in convs {
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
                    if m.ts > scanned_latest {
                        scanned_latest = m.ts.clone();
                    }
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
        found.sort_by(|a, b| a.1.ts.cmp(&b.1.ts));
        if scanned_latest > c.last_seen {
            c.last_seen = scanned_latest;
        }
        if !found.is_empty() {
            for (cv, m) in found {
                let title = if cv.is_im {
                    let dm_user = names.get(&cv.user).map(String::as_str).unwrap_or(&cv.user);
                    format!("@{}", dm_user)
                } else if cv.is_mpim {
                    format!("MPIM {}", cv.name)
                } else {
                    format!("#{}", cv.name)
                };
                let sender = names
                    .get(&m.user)
                    .cloned()
                    .filter(|n| !n.is_empty())
                    .unwrap_or_else(|| {
                        if m.username.is_empty() {
                            m.user.clone()
                        } else {
                            m.username.clone()
                        }
                    });
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
                for attachment in download_message_attachments(&s, &m).await? {
                    println!("  {}", attachment.display());
                }
            }
            save_config(&c)?;
            return Ok(());
        }
        if !wait {
            save_config(&c)?;
            return Ok(());
        }
        if wait {
            if c.app_token.is_empty() {
                return Err(Err::new(
                    "slackie read --wait requires an app-level xapp- token; run slackie setup",
                ));
            }
            c.last_seen = socket_mode_wait(&c, &names).await?;
            save_config(&c)?;
            return Ok(());
        }
    }
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
        let response = s
            .http
            .get(&url)
            .bearer_auth(&s.token)
            .send()
            .await
            .wrap("download message attachment")?;
        if !response.status().is_success() {
            return Err(Err::from_error(format!(
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
        let bytes = response.bytes().await.wrap("read attachment response")?;
        fs::write(&path, bytes).wrap("write downloaded attachment")?;
        paths.push(path);
    }
    Ok(paths)
}

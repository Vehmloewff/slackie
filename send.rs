use crate::{
    config::{load_config, token},
    slack::Slack,
    unreads::{list_convs, list_users},
};
use err::{Err, Result, ResultExt};
use serde_json::{json, Value};
use std::{fs, path::PathBuf};
use tokio::io::AsyncReadExt;
pub(crate) async fn send(args: &[String]) -> Result<()> {
    let mut target = String::new();
    let mut files = vec![];
    let mut mrkdwn = true;
    let mut i = 0;
    while i < args.len() {
        match args[i].as_str() {
            "--no-mrkdwn" => mrkdwn = false,
            "--attach" => {
                i += 1;
                if i >= args.len() {
                    return Err(Err::new("usage: slackie send [--attach PATH] <target>"));
                }
                files.push(args[i].clone())
            }
            v if v.starts_with("--attach=") => files.push(v[9..].to_string()),
            v if target.is_empty() => target = v.into(),
            _ => {
                return Err(Err::new(
                    "usage: slackie send [--attach PATH ...] [--no-mrkdwn] <target>",
                ))
            }
        }
        i += 1
    }
    if target.is_empty() {
        return Err(Err::new("send requires target"));
    }
    let mut text = String::new();
    tokio::io::stdin()
        .read_to_string(&mut text)
        .await
        .wrap("read stdin")?;
    let mut text = text.trim_end_matches(['\r', '\n']).to_string();
    if text.trim().is_empty() && files.is_empty() {
        return Err(Err::new("nothing to send"));
    }
    let c = load_config()?;
    let s = Slack::new(token(&c))?;
    text = resolve_user_mentions(&s, &text).await?;
    let (base, thread) = split_thread(&target);
    let (channel, title) = if let Some(n) = base.strip_prefix('#') {
        let cs = list_convs(&s, &c).await?;
        let cv = cs
            .iter()
            .find(|x| x.name == n)
            .ok_or_else(|| Err::from_error(format!("channel not found: {base}")))?;
        (cv.id.clone(), base.to_string())
    } else if base.starts_with('@') {
        let u = base.trim_start_matches('@');
        let us = list_users(&s).await?;
        let found = us
            .iter()
            .find(|x| {
                x.name.eq_ignore_ascii_case(u)
                    || x.real_name.eq_ignore_ascii_case(u)
                    || x.profile.display_name.eq_ignore_ascii_case(u)
            })
            .ok_or_else(|| Err::from_error(format!("user not found: {base}")))?;
        let d: Value = s
            .call("conversations.open", &[("users", found.id.clone())])
            .await?;
        (
            d["channel"]["id"].as_str().unwrap_or("").into(),
            base.to_string(),
        )
    } else {
        (base.to_string(), base.to_string())
    };
    if files.is_empty() {
        let mut form = vec![
            ("channel", channel.clone()),
            ("text", text),
            ("mrkdwn", mrkdwn.to_string()),
        ];
        if !thread.is_empty() {
            form.push(("thread_ts", thread.clone()));
        }
        let r: Value = s.call("chat.postMessage", &form).await?;
        println!(
            "sent to {title} (threading target: {}:{})",
            title,
            if thread.is_empty() {
                r["ts"].as_str().unwrap_or("")
            } else {
                &thread
            }
        );
    } else {
        if !mrkdwn && !text.is_empty() {
            let mut form = vec![
                ("channel", channel.clone()),
                ("text", text.clone()),
                ("mrkdwn", "false".into()),
            ];
            if !thread.is_empty() {
                form.push(("thread_ts", thread.clone()));
            }
            let posted: Value = s.call("chat.postMessage", &form).await?;
            if thread.is_empty() {
                if let Some(ts) = posted["ts"].as_str() {
                    println!("sent text to {title} (threading target: {title}:{ts})");
                }
            }
        }
        for (idx, path) in files.iter().enumerate() {
            let bytes = fs::read(path).wrap("read attachment")?;
            let filename = PathBuf::from(path)
                .file_name()
                .and_then(|n| n.to_str())
                .ok_or_else(|| Err::new("attachment has invalid filename"))?
                .to_owned();
            let length = bytes.len().to_string();
            let upload: Value = s
                .call(
                    "files.getUploadURLExternal",
                    &[("filename", filename.clone()), ("length", length)],
                )
                .await?;
            let upload_url = upload["upload_url"]
                .as_str()
                .ok_or_else(|| Err::new("Slack returned no upload URL"))?;
            let file_id = upload["file_id"]
                .as_str()
                .ok_or_else(|| Err::new("Slack returned no file ID"))?;
            let response = s
                .http
                .post(upload_url)
                .header(reqwest::header::CONTENT_TYPE, "application/octet-stream")
                .body(bytes)
                .send()
                .await
                .wrap("upload attachment bytes")?;
            if !response.status().is_success() {
                return Err(Err::from_error(format!(
                    "attachment upload failed: HTTP {}",
                    response.status()
                )));
            }
            let files_json = serde_json::to_string(&json!([{"id":file_id,"title":filename}]))
                .wrap("encode upload completion")?;
            let mut form = vec![("files", files_json), ("channel_id", channel.clone())];
            if !thread.is_empty() {
                form.push(("thread_ts", thread.clone()));
            }
            if idx == 0 && !text.is_empty() && mrkdwn {
                form.push(("initial_comment", text.clone()));
            }
            let completed: Value = s.call("files.completeUploadExternal", &form).await?;
            let ts = if !thread.is_empty() {
                Some(thread.as_str())
            } else {
                file_share_ts(&completed, &channel)
            };
            if let Some(ts) = ts {
                println!("uploaded {path} to {title} (threading target: {title}:{ts})");
            } else {
                println!("uploaded {path} to {title} (file id: {file_id})");
            }
        }
    }
    Ok(())
}
async fn resolve_user_mentions(s: &Slack, text: &str) -> Result<String> {
    if !text.contains("<@") {
        return Ok(text.to_owned());
    }
    let users = list_users(s)
        .await
        .wrap("load users for mention resolution")?;
    let mut output = String::with_capacity(text.len());
    let mut rest = text;
    while let Some(start) = rest.find("<@") {
        output.push_str(&rest[..start]);
        let mention = &rest[start + 2..];
        let Some(end) = mention.find('>') else {
            output.push_str(&rest[start..]);
            return Ok(output);
        };
        let raw = &mention[..end];
        let id = raw.split('|').next().unwrap_or(raw).trim();
        if (id.starts_with('U') || id.starts_with('W'))
            && id
                .chars()
                .all(|c| c.is_ascii_uppercase() || c.is_ascii_digit())
        {
            output.push_str(&rest[start..start + end + 3]);
        } else {
            let name = id.strip_prefix('@').unwrap_or(id).trim();
            let matches: Vec<_> = users
                .iter()
                .filter(|u| {
                    !u.deleted
                        && [
                            u.name.as_str(),
                            u.real_name.as_str(),
                            u.profile.real_name.as_str(),
                            u.profile.display_name.as_str(),
                            u.profile.display_name_normalized.as_str(),
                        ]
                        .iter()
                        .any(|candidate| candidate.eq_ignore_ascii_case(name))
                })
                .collect();
            if matches.len() != 1 {
                return Err(Err::from_error(if matches.is_empty() {
                    format!("User not found: {name}")
                } else {
                    format!("multiple users matched <@{name}>")
                }));
            }
            output.push_str(&format!("<@{}>", matches[0].id));
        }
        rest = &mention[end + 1..];
    }
    output.push_str(rest);
    Ok(output)
}

pub(crate) fn file_share_ts<'a>(response: &'a Value, channel: &str) -> Option<&'a str> {
    for visibility in ["public", "private"] {
        let shares = &response["files"][0]["shares"][visibility];
        let entries = shares[channel]
            .as_array()
            .or_else(|| shares.as_object()?.values().find_map(Value::as_array));
        if let Some(entries) = entries {
            for entry in entries {
                if let Some(ts) = entry["thread_ts"].as_str().filter(|ts| !ts.is_empty()) {
                    return Some(ts);
                }
                if let Some(ts) = entry["ts"].as_str().filter(|ts| !ts.is_empty()) {
                    return Some(ts);
                }
            }
        }
    }
    None
}

pub(crate) fn split_thread(t: &str) -> (&str, String) {
    if let Some((a, b)) = t.rsplit_once(':') {
        if b.contains('.') && b.chars().all(|c| c.is_ascii_digit() || c == '.') {
            return (a, b.into());
        }
    }
    (t, String::new())
}

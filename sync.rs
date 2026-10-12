use crate::config::load_config;
use crate::db::Db;
use crate::error::Result;
use crate::lists::{list_convs_all, list_users};
use crate::slack::{History, Msg, Slack};

/// One full pass over the workspace: refresh users, then walk every
/// conversation the account belongs to and insert anything the store is
/// missing. Runs at daemon startup, periodically, and on `slackie sync`.
pub async fn sync_all(db: &Db) -> Result<String> {
    let cfg = load_config()?;
    let slack = Slack::for_config(&cfg)?;
    let users = list_users(&slack).await?;
    db.upsert_users(&users).await?;
    let convs = list_convs_all(&slack, &cfg).await?;
    for cv in &convs {
        db.upsert_channel(cv).await?;
    }
    let mut messages = 0usize;
    for cv in &convs {
        match sync_channel(db, &slack, cv).await {
            Ok(n) => messages += n,
            Err(e) => log::warn!("sync {}: skipping conversation: {e:?}", cv.id),
        }
    }
    Ok(format!(
        "synced {} conversations, {} new messages",
        convs.len(),
        messages
    ))
}

/// Backfill one conversation: page through history from the newest message
/// already stored (or from the beginning on first sync), then fetch thread
/// replies for every thread discovered along the way.
async fn sync_channel(db: &Db, slack: &Slack, cv: &crate::slack::Conv) -> Result<usize> {
    let max = db.channel_max_ts(&cv.id).await?;
    if !max.is_empty() && !cv.latest.ts.is_empty() && cv.latest.ts <= max {
        return Ok(0);
    }
    let mut inserted = 0usize;
    let mut cursor = String::new();
    let mut threads: Vec<String> = Vec::new();
    loop {
        let mut p = vec![("channel", cv.id.clone()), ("limit", "200".into())];
        if !max.is_empty() {
            p.push(("oldest", max.clone()));
            p.push(("inclusive", "false".into()));
        }
        if !cursor.is_empty() {
            p.push(("cursor", cursor.clone()));
        }
        let h: History = match slack.get("conversations.history", &p).await {
            Ok(h) => h,
            Err(e) if format!("{e:?}").contains("not_in_channel") => return Ok(inserted),
            Err(e) => return Err(e),
        };
        for m in &h.messages {
            if !m.thread_ts.is_empty() && m.thread_ts == m.ts && m.reply_count > 0 {
                threads.push(m.ts.clone());
            }
            if db.upsert_message(&cv.id, m).await? {
                inserted += 1;
            }
        }
        cursor = h.response_metadata.next_cursor;
        if cursor.is_empty() {
            break;
        }
    }
    for ts in threads {
        inserted += sync_thread(db, slack, &cv.id, &ts).await?;
    }
    Ok(inserted)
}

/// Fetch every reply in a thread and insert the missing ones.
async fn sync_thread(db: &Db, slack: &Slack, channel: &str, ts: &str) -> Result<usize> {
    let mut inserted = 0usize;
    let mut cursor = String::new();
    loop {
        let mut p = vec![
            ("channel", channel.to_owned()),
            ("ts", ts.to_owned()),
            ("limit", "200".into()),
        ];
        if !cursor.is_empty() {
            p.push(("cursor", cursor.clone()));
        }
        let h: History = match slack.get("conversations.replies", &p).await {
            Ok(h) => h,
            Err(e) if format!("{e:?}").contains("not_in_channel") => return Ok(inserted),
            Err(e) => return Err(e),
        };
        for m in &h.messages {
            let mut m: Msg = m.clone();
            if m.thread_ts.is_empty() {
                m.thread_ts = ts.to_owned();
            }
            if db.upsert_message(channel, &m).await? {
                inserted += 1;
            }
        }
        cursor = h.response_metadata.next_cursor;
        if cursor.is_empty() {
            break;
        }
    }
    Ok(inserted)
}

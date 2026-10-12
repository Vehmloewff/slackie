use crate::config::Config;
use crate::error::Result;
use crate::slack::{Conv, List, Slack, User, Users};

/// Conversations the user or bot may read, gated by the configured
/// read preferences.
pub async fn list_convs(s: &Slack, c: &Config) -> Result<Vec<Conv>> {
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
    list_convs_typed(s, c, &types).await
}

/// Every conversation the user or bot is a member of, regardless of the
/// read preferences. Used to backfill the local message store.
pub async fn list_convs_all(s: &Slack, c: &Config) -> Result<Vec<Conv>> {
    list_convs_typed(s, c, &["public_channel", "private_channel", "im", "mpim"]).await
}

async fn list_convs_typed(s: &Slack, c: &Config, types: &[&str]) -> Result<Vec<Conv>> {
    let types = types.join(",");
    if crate::config::is_session(c) {
        let user = if c.user_id.is_empty() {
            c.bot_user_id.clone()
        } else {
            c.user_id.clone()
        };
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
    let mut cursor = String::new();
    let mut all = vec![];
    loop {
        let mut p = vec![
            ("types", types.clone()),
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

pub async fn list_users(s: &Slack) -> Result<Vec<User>> {
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

use crate::error::{error_report, AppError, Result};
use crate::slack::{Conv, Msg, SlackAttachment, SlackFile, User};
use error_stack::{Report, ResultExt};
use std::{path::Path, sync::Arc};
use tokio::sync::Mutex;
use turso::{Builder, Connection, Database, IntoParams, Row};

const SCHEMA: &str = "
CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL DEFAULT '',
    real_name TEXT NOT NULL DEFAULT '',
    display_name TEXT NOT NULL DEFAULT '',
    deleted INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS channels (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL DEFAULT '',
    user TEXT NOT NULL DEFAULT '',
    is_im INTEGER NOT NULL DEFAULT 0,
    is_mpim INTEGER NOT NULL DEFAULT 0,
    is_group INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS messages (
    channel_id TEXT NOT NULL,
    ts TEXT NOT NULL,
    thread_ts TEXT NOT NULL DEFAULT '',
    user TEXT NOT NULL DEFAULT '',
    text TEXT NOT NULL DEFAULT '',
    subtype TEXT NOT NULL DEFAULT '',
    username TEXT NOT NULL DEFAULT '',
    bot_id TEXT NOT NULL DEFAULT '',
    files TEXT NOT NULL DEFAULT '[]',
    attachments TEXT NOT NULL DEFAULT '[]',
    PRIMARY KEY (channel_id, ts)
);
CREATE INDEX IF NOT EXISTS messages_ts ON messages (ts);
CREATE TABLE IF NOT EXISTS state (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL DEFAULT ''
);
";

fn db_err(e: turso::Error) -> Report<AppError> {
    Report::new(e).change_context(AppError::Context("message store"))
}

/// Local message store backed by the Turso database (the SQLite rewrite).
///
/// One connection is shared by every daemon task; the internal lock keeps
/// queries from interleaving with writes mid-scan.
pub struct Db {
    _db: Database,
    conn: Connection,
    lock: Mutex<()>,
}

impl Db {
    pub async fn open(path: &Path) -> Result<Arc<Db>> {
        let p = path
            .to_str()
            .ok_or_else(|| error_report("database path is not valid UTF-8"))?;
        let db = Builder::new_local(p)
            .build()
            .await
            .map_err(db_err)
            .change_context(AppError::Context("open message store"))?;
        let conn = db
            .connect()
            .map_err(db_err)
            .change_context(AppError::Context("open message store"))?;
        let store = Arc::new(Db {
            _db: db,
            conn,
            lock: Mutex::new(()),
        });
        store.batch(SCHEMA).await?;
        Ok(store)
    }

    /// Drop every table (used when the configured workspace changes).
    pub async fn reset(&self) -> Result<()> {
        let sql = "
            DROP TABLE IF EXISTS messages;
            DROP TABLE IF EXISTS channels;
            DROP TABLE IF EXISTS users;
            DROP TABLE IF EXISTS state;";
        self.batch(sql).await?;
        self.batch(SCHEMA).await?;
        Ok(())
    }

    async fn batch(&self, sql: &str) -> Result<()> {
        let _guard = self.lock.lock().await;
        self.conn
            .execute_batch(sql)
            .await
            .map_err(db_err)
            .change_context(AppError::Context("initialize message store"))
    }

    async fn exec(&self, sql: &str, params: impl IntoParams) -> Result<u64> {
        let _guard = self.lock.lock().await;
        self.conn
            .execute(sql, params)
            .await
            .map_err(db_err)
            .change_context(AppError::Context("write message store"))
    }

    async fn rows<T>(
        &self,
        sql: &str,
        params: impl IntoParams,
        mut visit: impl FnMut(&Row) -> Result<T>,
    ) -> Result<Vec<T>> {
        let _guard = self.lock.lock().await;
        let mut rows = self
            .conn
            .query(sql, params)
            .await
            .map_err(db_err)
            .change_context(AppError::Context("query message store"))?;
        let mut out = Vec::new();
        while let Some(row) = rows
            .next()
            .await
            .map_err(db_err)
            .change_context(AppError::Context("query message store"))?
        {
            out.push(visit(&row)?);
        }
        Ok(out)
    }

    pub async fn upsert_user(&self, u: &User) -> Result<()> {
        self.exec(
            "INSERT INTO users (id, name, real_name, display_name, deleted)
             VALUES (?1, ?2, ?3, ?4, ?5)
             ON CONFLICT(id) DO UPDATE SET
                name = excluded.name,
                real_name = excluded.real_name,
                display_name = excluded.display_name,
                deleted = excluded.deleted",
            (
                u.id.clone(),
                u.name.clone(),
                u.real_name.clone(),
                u.profile.display_name.clone(),
                u.deleted as i64,
            ),
        )
        .await?;
        Ok(())
    }

    pub async fn upsert_users(&self, users: &[User]) -> Result<()> {
        for u in users {
            self.upsert_user(u).await?;
        }
        Ok(())
    }

    pub async fn upsert_channel(&self, c: &Conv) -> Result<()> {
        self.exec(
            "INSERT INTO channels (id, name, user, is_im, is_mpim, is_group)
             VALUES (?1, ?2, ?3, ?4, ?5, ?6)
             ON CONFLICT(id) DO UPDATE SET
                name = excluded.name,
                user = excluded.user,
                is_im = excluded.is_im,
                is_mpim = excluded.is_mpim,
                is_group = excluded.is_group",
            (
                c.id.clone(),
                c.name.clone(),
                c.user.clone(),
                c.is_im as i64,
                c.is_mpim as i64,
                c.is_group as i64,
            ),
        )
        .await?;
        Ok(())
    }

    /// Register a channel seen only through the event stream, without
    /// clobbering details that a conversation sync has stored.
    pub async fn ensure_channel(
        &self,
        id: &str,
        is_im: bool,
        is_mpim: bool,
        is_group: bool,
    ) -> Result<()> {
        self.exec(
            "INSERT OR IGNORE INTO channels (id, name, user, is_im, is_mpim, is_group)
             VALUES (?1, '', '', ?2, ?3, ?4)",
            (id.to_owned(), is_im as i64, is_mpim as i64, is_group as i64),
        )
        .await?;
        Ok(())
    }

    /// Insert or update a message. Returns true when the row is new or its
    /// contents changed, false when the stored copy was already identical.
    pub async fn upsert_message(&self, channel_id: &str, m: &Msg) -> Result<bool> {
        let files = serde_json::to_string(&m.files)
            .map_err(Report::new)
            .change_context(AppError::Context("encode message files"))?;
        let attachments = serde_json::to_string(&m.attachments)
            .map_err(Report::new)
            .change_context(AppError::Context("encode message attachments"))?;
        let n = self
            .exec(
                "INSERT INTO messages
                    (channel_id, ts, thread_ts, user, text, subtype, username, bot_id, files, attachments)
                 VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10)
                 ON CONFLICT(channel_id, ts) DO UPDATE SET
                    thread_ts = excluded.thread_ts,
                    user = excluded.user,
                    text = excluded.text,
                    subtype = excluded.subtype,
                    username = excluded.username,
                    bot_id = excluded.bot_id,
                    files = excluded.files,
                    attachments = excluded.attachments
                 WHERE messages.thread_ts IS NOT excluded.thread_ts
                    OR messages.user IS NOT excluded.user
                    OR messages.text IS NOT excluded.text
                    OR messages.subtype IS NOT excluded.subtype
                    OR messages.username IS NOT excluded.username
                    OR messages.bot_id IS NOT excluded.bot_id
                    OR messages.files IS NOT excluded.files
                    OR messages.attachments IS NOT excluded.attachments",
                (
                    channel_id.to_owned(),
                    m.ts.clone(),
                    m.thread_ts.clone(),
                    m.user.clone(),
                    m.text.clone(),
                    m.subtype.clone(),
                    m.username.clone(),
                    m.bot_id.clone(),
                    files,
                    attachments,
                ),
            )
            .await?;
        Ok(n > 0)
    }

    pub async fn channels(&self) -> Result<Vec<Conv>> {
        self.rows(
            "SELECT id, name, user, is_im, is_mpim, is_group FROM channels ORDER BY name",
            (),
            row_to_conv,
        )
        .await
    }

    pub async fn find_channel_by_name(&self, name: &str) -> Result<Option<Conv>> {
        Ok(self
            .rows(
                "SELECT id, name, user, is_im, is_mpim, is_group FROM channels WHERE name = ?1",
                (name,),
                row_to_conv,
            )
            .await?
            .into_iter()
            .next())
    }

    pub async fn channel_max_ts(&self, channel_id: &str) -> Result<String> {
        let mut found = self
            .rows(
                "SELECT COALESCE(MAX(ts), '') FROM messages WHERE channel_id = ?1",
                (channel_id,),
                |row| text(row, 0),
            )
            .await?;
        Ok(found.pop().unwrap_or_default())
    }

    /// All messages newer than `oldest`, oldest first, each joined with its
    /// channel (channels unknown to the store yield an id-only row).
    pub async fn messages_after(&self, oldest: &str) -> Result<Vec<(Conv, Msg)>> {
        struct Pair(Conv, Msg);
        let pairs = self
            .rows(
                "SELECT m.channel_id, m.ts, m.thread_ts, m.user, m.text, m.subtype,
                        m.username, m.bot_id, m.files, m.attachments,
                        COALESCE(c.name, ''), COALESCE(c.user, ''),
                        COALESCE(c.is_im, 0), COALESCE(c.is_mpim, 0), COALESCE(c.is_group, 0)
                 FROM messages m
                 LEFT JOIN channels c ON c.id = m.channel_id
                 WHERE m.ts > ?1
                 ORDER BY m.ts ASC",
                (oldest,),
                |row| {
                    let files =
                        serde_json::from_str::<Vec<SlackFile>>(&text(row, 9)?).unwrap_or_default();
                    let attachments = serde_json::from_str::<Vec<SlackAttachment>>(&text(row, 10)?)
                        .unwrap_or_default();
                    Ok(Pair(
                        Conv {
                            id: text(row, 0)?,
                            name: text(row, 10)?,
                            user: text(row, 11)?,
                            is_im: num(row, 12)? != 0,
                            is_mpim: num(row, 13)? != 0,
                            is_group: num(row, 14)? != 0,
                            ..Default::default()
                        },
                        Msg {
                            ts: text(row, 1)?,
                            thread_ts: text(row, 2)?,
                            user: text(row, 3)?,
                            text: text(row, 4)?,
                            subtype: text(row, 5)?,
                            username: text(row, 6)?,
                            bot_id: text(row, 7)?,
                            files,
                            attachments,
                            ..Default::default()
                        },
                    ))
                },
            )
            .await?;
        Ok(pairs.into_iter().map(|Pair(c, m)| (c, m)).collect())
    }

    pub async fn user(&self, id: &str) -> Result<Option<User>> {
        Ok(self
            .rows(
                "SELECT id, name, real_name, display_name, deleted FROM users WHERE id = ?1",
                (id,),
                row_to_user,
            )
            .await?
            .into_iter()
            .next())
    }

    pub async fn users(&self) -> Result<Vec<User>> {
        self.rows(
            "SELECT id, name, real_name, display_name, deleted FROM users ORDER BY name",
            (),
            row_to_user,
        )
        .await
    }

    pub async fn state_get(&self, key: &str) -> Result<Option<String>> {
        Ok(self
            .rows("SELECT value FROM state WHERE key = ?1", (key,), |row| {
                text(row, 0)
            })
            .await?
            .into_iter()
            .next())
    }

    pub async fn state_set(&self, key: &str, value: &str) -> Result<()> {
        self.exec(
            "INSERT INTO state (key, value) VALUES (?1, ?2)
             ON CONFLICT(key) DO UPDATE SET value = excluded.value",
            (key.to_owned(), value.to_owned()),
        )
        .await?;
        Ok(())
    }
}

fn text(row: &Row, i: usize) -> Result<String> {
    row.get::<String>(i)
        .map_err(db_err)
        .change_context(AppError::Context("read message store"))
}

fn num(row: &Row, i: usize) -> Result<i64> {
    row.get::<i64>(i)
        .map_err(db_err)
        .change_context(AppError::Context("read message store"))
}

fn row_to_conv(row: &Row) -> Result<Conv> {
    Ok(Conv {
        id: text(row, 0)?,
        name: text(row, 1)?,
        user: text(row, 2)?,
        is_im: num(row, 3)? != 0,
        is_mpim: num(row, 4)? != 0,
        is_group: num(row, 5)? != 0,
        ..Default::default()
    })
}

fn row_to_user(row: &Row) -> Result<User> {
    let display_name = text(row, 3)?;
    Ok(User {
        id: text(row, 0)?,
        name: text(row, 1)?,
        real_name: text(row, 2)?,
        profile: crate::slack::Profile {
            display_name: display_name.clone(),
            display_name_normalized: display_name,
            ..Default::default()
        },
        deleted: num(row, 4)? != 0,
    })
}

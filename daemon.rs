use crate::config::{db_path, socket_path};
use crate::db::Db;
use crate::error::{error_report, AppError, Result};
use crate::ipc::{Request, Response};
use crate::read::{ensure_last_seen, read};
use crate::send::send;
use crate::slack::Msg;
use crate::socket::{self, Event};
use crate::sync::sync_all;
use error_stack::{Report, ResultExt};
use std::time::Instant;
use std::{fs, sync::Arc, time::Duration};
use tokio::io::{AsyncBufReadExt, AsyncWriteExt, BufReader};
use tokio::net::{UnixListener, UnixStream};
use tokio::sync::{mpsc, watch, Mutex};

/// Full workspace sync cadence; the first pass runs at startup.
const SYNC_INTERVAL: Duration = Duration::from_secs(300);

pub struct Daemon {
    db: Arc<Db>,
    /// Bumped whenever new messages land in the store, so blocked
    /// `slackie read --wait` requests wake up.
    rev: watch::Sender<u64>,
    sync_lock: Mutex<()>,
}

/// Run the daemon: open the store, start the backfill loop and the live
/// event listener, then serve client requests on the unix socket forever.
pub async fn run() -> Result<()> {
    let path = db_path()?;
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent)
            .map_err(Report::new)
            .change_context(AppError::Context("create state directory"))?;
    }
    let _lock = single_instance()?;
    let db = Db::open(&path).await?;
    if let Ok(cfg) = crate::config::load_config() {
        reconcile_team(&db, &cfg).await?;
        ensure_last_seen(&db, &cfg).await?;
    }
    let (rev, _) = watch::channel(0u64);
    let daemon = Arc::new(Daemon {
        db,
        rev,
        sync_lock: Mutex::new(()),
    });
    tokio::spawn(event_task(daemon.clone()));
    tokio::spawn(sync_task(daemon.clone()));
    serve(daemon).await
}

impl Daemon {
    fn bump(&self) {
        self.rev.send_modify(|v| *v = v.wrapping_add(1));
    }

    async fn sync_now(&self) -> Result<String> {
        let _guard = self.sync_lock.lock().await;
        let summary = sync_all(&self.db).await?;
        self.bump();
        Ok(summary + "\n")
    }

    /// Record a message that arrived on the live event stream.
    async fn insert_event(&self, ev: &Event) -> Result<()> {
        self.db
            .ensure_channel(
                &ev.channel,
                ev.channel_type == "im",
                ev.channel_type == "mpim",
                ev.channel_type == "group",
            )
            .await?;
        let m = Msg {
            ts: ev.ts.clone(),
            user: ev.user.clone(),
            text: ev.text.clone(),
            subtype: ev.subtype.clone(),
            thread_ts: ev.thread_ts.clone(),
            bot_id: ev.bot_id.clone(),
            ..Default::default()
        };
        self.db.upsert_message(&ev.channel, &m).await?;
        Ok(())
    }

    async fn dispatch(&self, req: &Request) -> Result<String> {
        match req.cmd.as_str() {
            "read" => {
                if req.args.len() > 1 || req.args.iter().any(|a| a != "--wait") {
                    return Err(error_report("usage: slackie read [--wait]"));
                }
                let wait = req.args.iter().any(|a| a == "--wait");
                let mut rev = self.rev.subscribe();
                read(&self.db, wait, &mut rev).await
            }
            "send" => send(&self.db, &req.args, &req.text).await,
            "sync" => self.sync_now().await,
            "reload" => {
                if let Ok(cfg) = crate::config::load_config() {
                    reconcile_team(&self.db, &cfg).await?;
                }
                Ok(String::new())
            }
            other => Err(error_report(format!("unknown command: {other}"))),
        }
    }
}

/// Hold an exclusive lock so a second `slackied` (for example one spawned
/// by a client racing another daemon) exits instead of stealing the socket.
fn single_instance() -> Result<fs::File> {
    let dir = crate::config::config_dir()?;
    fs::create_dir_all(&dir)
        .map_err(Report::new)
        .change_context(AppError::Context("create config directory"))?;
    let path = dir.join("slackied.lock");
    let file = fs::OpenOptions::new()
        .create(true)
        .append(true)
        .open(&path)
        .map_err(Report::new)
        .change_context(AppError::Context("open daemon lock file"))?;
    if file.try_lock().is_err() {
        return Err(error_report(format!(
            "slackied is already running (lock: {})",
            path.display()
        )));
    }
    Ok(file)
}

/// Wipe the store when the configured workspace changes.
async fn reconcile_team(db: &Db, cfg: &crate::config::Config) -> Result<()> {
    let stored = db.state_get("team_id").await?.unwrap_or_default();
    if !stored.is_empty() && !cfg.team_id.is_empty() && stored != cfg.team_id {
        log::info!(
            "workspace changed from {stored} to {}: resetting message store",
            cfg.team_id
        );
        db.reset().await?;
    }
    if !cfg.team_id.is_empty() {
        db.state_set("team_id", &cfg.team_id).await?;
    }
    Ok(())
}

/// Keep the event stream connected forever, backing off between attempts,
/// and insert every message event into the store.
async fn event_task(d: Arc<Daemon>) {
    let (tx, mut rx) = mpsc::channel::<Event>(256);
    let _keep_alive = tx.clone();
    tokio::spawn(async move {
        let mut backoff = Duration::from_secs(1);
        loop {
            let outcome = match crate::config::load_config() {
                Ok(cfg) => socket::listen(&cfg, &tx).await,
                Err(e) => Err(e),
            };
            match outcome {
                Ok(()) => backoff = Duration::from_secs(1),
                Err(e) => log::warn!("event stream ended: {e:?}"),
            }
            tokio::time::sleep(backoff).await;
            backoff = (backoff * 2).min(Duration::from_secs(60));
        }
    });
    while let Some(ev) = rx.recv().await {
        match d.insert_event(&ev).await {
            Ok(()) => d.bump(),
            Err(e) => log::warn!("cannot store message {}: {e:?}", ev.channel),
        }
    }
}

/// Run a full workspace sync at startup, then periodically so restarts and
/// missed events always converge.
async fn sync_task(d: Arc<Daemon>) {
    loop {
        let started = Instant::now();
        match d.sync_now().await {
            Ok(summary) => log::info!("{} ({:?})", summary.trim(), started.elapsed()),
            Err(e) => log::warn!("sync failed: {e:?}"),
        }
        tokio::time::sleep(SYNC_INTERVAL).await;
    }
}

/// Serve client requests on the daemon socket, one request per connection.
async fn serve(d: Arc<Daemon>) -> Result<()> {
    let path = socket_path()?;
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent)
            .map_err(Report::new)
            .change_context(AppError::Context("create config directory"))?;
    }
    let _ = fs::remove_file(&path);
    let listener = UnixListener::bind(&path)
        .map_err(Report::new)
        .change_context(AppError::Context("bind daemon socket"))?;
    log::info!("slackied listening on {}", path.display());
    loop {
        let (stream, _) = listener
            .accept()
            .await
            .map_err(Report::new)
            .change_context(AppError::Context("accept daemon connection"))?;
        let d = d.clone();
        tokio::spawn(async move {
            if let Err(e) = handle(d, stream).await {
                log::warn!("request failed: {e:?}");
            }
        });
    }
}

async fn handle(d: Arc<Daemon>, stream: UnixStream) -> Result<()> {
    let (rd, mut wr) = stream.into_split();
    let mut line = String::new();
    let read = BufReader::new(rd)
        .read_line(&mut line)
        .await
        .map_err(Report::new)
        .change_context(AppError::Context("read daemon request"))?;
    if read == 0 {
        return Ok(());
    }
    let response = match serde_json::from_str::<Request>(&line) {
        Ok(req) => match d.dispatch(&req).await {
            Ok(output) => Response {
                ok: true,
                output,
                error: String::new(),
            },
            Err(e) => Response {
                ok: false,
                output: String::new(),
                error: format!("{e:?}"),
            },
        },
        Err(e) => Response {
            ok: false,
            output: String::new(),
            error: format!("bad request: {e}"),
        },
    };
    let mut payload = serde_json::to_string(&response)
        .map_err(Report::new)
        .change_context(AppError::Context("encode daemon response"))?;
    payload.push('\n');
    wr.write_all(payload.as_bytes())
        .await
        .map_err(Report::new)
        .change_context(AppError::Context("write daemon response"))?;
    wr.flush()
        .await
        .map_err(Report::new)
        .change_context(AppError::Context("write daemon response"))?;
    Ok(())
}

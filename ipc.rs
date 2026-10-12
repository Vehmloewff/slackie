use crate::config::socket_path;
use crate::error::{error_report, Result};
use serde::{Deserialize, Serialize};
use std::path::PathBuf;
use std::process::{Command, Stdio};
use std::time::Duration;
use tokio::io::{AsyncBufReadExt, AsyncWriteExt, BufReader};
use tokio::net::UnixStream;
use tokio::time::{timeout, Instant};

/// One client request over the daemon socket. One request per connection.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Request {
    pub cmd: String,
    #[serde(default)]
    pub args: Vec<String>,
    #[serde(default)]
    pub text: String,
}

impl Request {
    pub fn new(cmd: &str) -> Self {
        Self {
            cmd: cmd.to_owned(),
            args: Vec::new(),
            text: String::new(),
        }
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Response {
    pub ok: bool,
    #[serde(default)]
    pub output: String,
    #[serde(default)]
    pub error: String,
}

const CONNECT_TIMEOUT: Duration = Duration::from_secs(5);
const STARTUP_GRACE: Duration = Duration::from_secs(15);
const RETRY_INTERVAL: Duration = Duration::from_millis(250);

/// Send a request to the daemon. If the daemon is not running, start
/// `slackied` and wait for it to come up.
pub async fn call(req: &Request) -> Result<Response> {
    let path = socket_path()?;
    let payload = encode(req)?;
    match connect(&path).await {
        Ok(stream) => exchange(stream, &payload).await,
        Err(connect_error) => {
            if spawn_daemon().is_err() {
                return Err(connect_error);
            }
            log::debug!(
                "started slackied, waiting for it to listen on {}",
                path.display()
            );
            let deadline = Instant::now() + STARTUP_GRACE;
            loop {
                tokio::time::sleep(RETRY_INTERVAL).await;
                match connect(&path).await {
                    Ok(stream) => return exchange(stream, &payload).await,
                    Err(e) if Instant::now() >= deadline => return Err(e),
                    Err(_) => {}
                }
            }
        }
    }
}

/// Best-effort nudge telling a running daemon that the config changed.
/// Never starts the daemon.
pub async fn reload() {
    let Ok(path) = socket_path() else { return };
    let Ok(payload) = encode(&Request::new("reload")) else {
        return;
    };
    if let Ok(stream) = connect(&path).await {
        let _ = exchange(stream, &payload).await;
    }
}

fn encode(req: &Request) -> Result<String> {
    let mut payload =
        serde_json::to_string(req).map_err(|e| error_report(format!("encode request: {e}")))?;
    payload.push('\n');
    Ok(payload)
}

async fn connect(path: &PathBuf) -> Result<UnixStream> {
    timeout(CONNECT_TIMEOUT, UnixStream::connect(path))
        .await
        .map_err(|_| {
            error_report(format!(
                "cannot reach slackied on {}: timed out; start the daemon with `slackied`",
                path.display()
            ))
        })?
        .map_err(|e| {
            error_report(format!(
                "cannot reach slackied on {}: {e}; start the daemon with `slackied`",
                path.display()
            ))
        })
}

async fn exchange(mut stream: UnixStream, payload: &str) -> Result<Response> {
    stream
        .write_all(payload.as_bytes())
        .await
        .map_err(|e| error_report(format!("write to slackied: {e}")))?;
    stream
        .flush()
        .await
        .map_err(|e| error_report(format!("flush to slackied: {e}")))?;
    let mut line = String::new();
    BufReader::new(stream)
        .read_line(&mut line)
        .await
        .map_err(|e| error_report(format!("read from slackied: {e}")))?;
    if line.trim().is_empty() {
        return Err(error_report(
            "slackied closed the connection without responding",
        ));
    }
    serde_json::from_str::<Response>(line.trim())
        .map_err(|e| error_report(format!("decode slackied response: {e}")))
}

/// Locate the daemon binary next to this executable, then on PATH.
fn find_daemon() -> Result<PathBuf> {
    if let Ok(exe) = std::env::current_exe() {
        if let Some(dir) = exe.parent() {
            let candidate = dir.join("slackied");
            if is_executable(&candidate) {
                return Ok(candidate);
            }
        }
    }
    if let Some(paths) = std::env::var_os("PATH") {
        for dir in std::env::split_paths(&paths) {
            let candidate = dir.join("slackied");
            if is_executable(&candidate) {
                return Ok(candidate);
            }
        }
    }
    Err(error_report(
        "cannot find the slackied daemon next to slackie or on PATH",
    ))
}

#[cfg(unix)]
fn is_executable(path: &std::path::Path) -> bool {
    use std::os::unix::fs::PermissionsExt;
    path.metadata()
        .map(|m| m.is_file() && m.permissions().mode() & 0o111 != 0)
        .unwrap_or(false)
}

#[cfg(not(unix))]
fn is_executable(path: &std::path::Path) -> bool {
    path.is_file()
}

fn spawn_daemon() -> Result<()> {
    let exe = find_daemon()?;
    Command::new(&exe)
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .spawn()
        .map(|_| ())
        .map_err(|e| error_report(format!("spawn {}: {e}", exe.display())))
}

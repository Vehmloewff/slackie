use error_stack::{Report, ResultExt};
use slackie::{
    auth::setup,
    config::config_path,
    error::{error_report, AppError, Result},
    ipc::{self, Request},
};
use std::io::Write;
use std::{env, fs, io};
use tokio::io::AsyncReadExt;

/// slackie is the thin client: every command that touches Slack or the
/// message store is forwarded to the slackied daemon over its unix socket.
fn help() {
    println!("slackie - tiny Slack CLI (talks to the slackied daemon)\n\nUsage:\n  slackie setup\n  slackie reset\n  slackie read [--wait]\n  slackie send [--attach PATH ...] [--no-mrkdwn] <target>\n  slackie sync\n  slackie help\n\nThe daemon (slackied) stores users, channels, and messages locally and\nlistens for new messages; start it with `slackied` if it is not running.");
}

#[tokio::main]
async fn main() {
    env_logger::Builder::from_env(env_logger::Env::default().default_filter_or(
        if cfg!(debug_assertions) {
            "debug"
        } else {
            "info"
        },
    ))
    .init();
    if let Err(e) = run().await {
        eprintln!("error: {e:?}");
        std::process::exit(1)
    }
}

async fn run() -> Result<()> {
    let a: Vec<String> = env::args().skip(1).collect();
    match a.first().map(String::as_str) {
        None | Some("help") | Some("-h") | Some("--help") => {
            help();
            Ok(())
        }
        Some("setup") if a.len() == 1 => {
            setup().await?;
            ipc::reload().await;
            Ok(())
        }
        Some("reset") if a.len() == 1 => {
            let p = config_path()?;
            match fs::remove_file(&p) {
                Ok(()) => println!("Removed config: {}", p.display()),
                Err(e) if e.kind() == io::ErrorKind::NotFound => {
                    println!("No config found: {}", p.display())
                }
                Err(e) => {
                    return Err(Report::new(e).change_context(AppError::Context("remove config")))
                }
            }
            ipc::reload().await;
            Ok(())
        }
        Some("read") => {
            if a.len() > 2 || a.get(1).is_some_and(|x| x != "--wait") {
                return Err(error_report("usage: slackie read [--wait]"));
            }
            forward(Request {
                cmd: "read".into(),
                args: a[1..].to_vec(),
                text: String::new(),
            })
            .await
        }
        Some("send") => {
            let mut text = String::new();
            if a.len() >= 2 {
                tokio::io::stdin()
                    .read_to_string(&mut text)
                    .await
                    .map_err(Report::new)
                    .change_context(AppError::Context("read stdin"))?;
            }
            forward(Request {
                cmd: "send".into(),
                args: a[1..].to_vec(),
                text,
            })
            .await
        }
        Some("sync") if a.len() == 1 => forward(Request::new("sync")).await,
        Some("sync") => Err(error_report("usage: slackie sync")),
        Some(cmd) => Err(error_report(format!("unknown command: {cmd}"))),
    }
}

async fn forward(req: Request) -> Result<()> {
    let response = ipc::call(&req).await?;
    if !response.ok {
        return Err(error_report(response.error));
    }
    print!("{}", response.output);
    io::stdout()
        .flush()
        .map_err(Report::new)
        .change_context(AppError::Context("flush output"))?;
    Ok(())
}

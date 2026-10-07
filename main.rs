mod auth;
mod config;
mod manifest;
mod send;
mod slack;
mod socket;
mod unreads;

use auth::setup;
use config::config_path;
use err::{Err, Result};
use send::send;
use std::{env, fs, io};
use unreads::read;

#[cfg(test)]
mod tests {
    use crate::{
        config::Config,
        manifest::manifest,
        send::{file_share_ts, split_thread},
    };
    use serde_json::json;

    #[test]
    fn manifest_sets_selected_read_scopes_and_events() {
        let config = Config {
            read_app_mentions_only: false,
            read_dms: true,
            read_private_channels: false,
            read_message_groups: true,
            ..Config::default()
        };
        let value = manifest("Test Bot", &config);
        let scopes = value["oauth_config"]["scopes"]["bot"].as_array().unwrap();
        let events = value["settings"]["event_subscriptions"]["bot_events"]
            .as_array()
            .unwrap();
        assert!(scopes.iter().any(|v| v == "im:history"));
        assert!(!scopes.iter().any(|v| v == "groups:history"));
        assert!(events.iter().any(|v| v == "message.channels"));
        assert!(events.iter().any(|v| v == "message.mpim"));
        assert_eq!(value["display_information"]["name"], "Test Bot");
    }

    #[test]
    fn thread_target_splits_only_timestamp_suffix() {
        assert_eq!(
            split_thread("#channel:1740000000.123456"),
            ("#channel", "1740000000.123456".to_owned())
        );
        assert_eq!(
            split_thread("#channel:not-a-ts"),
            ("#channel:not-a-ts", String::new())
        );
    }

    #[test]
    fn completed_upload_share_provides_thread_timestamp() {
        let response =
            json!({"files":[{"shares":{"public":{"C123":[{"ts":"1740000000.123456"}]}}}]});
        assert_eq!(file_share_ts(&response, "C123"), Some("1740000000.123456"));
    }

    #[test]
    fn config_deserialization_preserves_legacy_read_defaults() {
        let config: Config = serde_json::from_str("{}").unwrap();
        assert!(
            config.read_app_mentions_only
                && config.read_dms
                && config.read_private_channels
                && config.read_message_groups
        );
    }
}

fn help() {
    println!("slackie - tiny Slack CLI\n\nUsage:\n  slackie setup\n  slackie reset\n  slackie read [--wait]\n  slackie send [--attach PATH ...] [--no-mrkdwn] <target>\n  slackie help");
}
#[tokio::main]
async fn main() {
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
        Some("setup") if a.len() == 1 => setup().await,
        Some("reset") if a.len() == 1 => {
            let p = config_path()?;
            match fs::remove_file(&p) {
                Ok(()) => println!("Removed config: {}", p.display()),
                Err(e) if e.kind() == io::ErrorKind::NotFound => {
                    println!("No config found: {}", p.display())
                }
                Err(e) => return Err(Err::from_error(e).wrap("remove config")),
            }
            Ok(())
        }
        Some("read") => {
            if a.len() > 2 || a.get(1).is_some_and(|x| x != "--wait") {
                return Err(Err::new("usage: slackie read [--wait]"));
            }
            read(a.get(1).is_some_and(|x| x == "--wait")).await
        }
        Some("send") => send(&a[1..]).await,
        Some(cmd) => Err(Err::from_error(format!("unknown command: {cmd}"))),
    }
}

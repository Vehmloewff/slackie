use crate::{
    config::{ask, config_path, save_config, Config},
    manifest::manifest,
    slack::{Auth, Slack},
};
use err::{Err, Result, ResultExt};
pub(crate) async fn setup() -> Result<()> {
    println!("Create a Slack app from a manifest, then paste its tokens.\n");
    let name = ask("Bot name: ")?;
    let mode = ask("Read mode [1 mention-only / 2 all channel messages]: ")?;
    if mode != "1" && mode != "2" {
        return Err(Err::new("choose 1 or 2"));
    }
    let yesno = |q: &str| -> Result<bool> {
        let a = ask(q)?;
        match a.to_lowercase().as_str() {
            "y" | "yes" => Ok(true),
            "n" | "no" => Ok(false),
            _ => Err(Err::new("answer y or n")),
        }
    };
    let mut c = Config {
        read_app_mentions_only: mode == "1",
        read_dms: yesno("Allow DMs? [y/n]: ")?,
        read_private_channels: yesno("Allow private channels? [y/n]: ")?,
        read_message_groups: yesno("Allow message groups? [y/n]: ")?,
        ..Default::default()
    };
    println!(
        "\nCreate app at https://api.slack.com/apps → From an app manifest.\n{}",
        serde_json::to_string_pretty(&manifest(&name, &c)).wrap("encode manifest")?
    );
    println!("\nPaste the manifest into Slack and create the app. Install it to your workspace and approve requested permissions, then copy the Bot User OAuth Token.");
    let bot = ask("Bot User OAuth Token (xoxb-...): ")?;
    if !bot.starts_with("xoxb-") {
        return Err(Err::new("invalid bot token: expected xoxb-"));
    }
    println!("In Basic Information → App-Level Tokens, create a token with connections:write.");
    let app = ask("App-level Socket Mode token (xapp-...): ")?;
    if !app.starts_with("xapp-") {
        return Err(Err::new("invalid app token: expected xapp-"));
    }
    let slack = Slack::new(&bot)?;
    let auth: Auth = slack
        .call("auth.test", &[])
        .await
        .wrap("verify bot token")?;
    c.access_token = bot.clone();
    c.bot_access_token = bot;
    c.app_token = app;
    c.bot_user_id = auth.user_id;
    c.team_id = auth.team_id;
    c.team_name = auth.team;
    c.last_seen = slack_ts();
    save_config(&c)?;
    println!("Setup complete. Config: {}", config_path()?.display());
    Ok(())
}
fn slack_ts() -> String {
    use std::time::{SystemTime, UNIX_EPOCH};
    let d = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default();
    format!("{}.{:06}", d.as_secs(), d.subsec_micros())
}

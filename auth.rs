use crate::error::{error_report, AppError, Result};
use crate::{
    config::{ask, config_path, save_config, Config},
    manifest::manifest,
    slack::{Auth, Slack},
};
use error_stack::{Report, ResultExt};
pub(crate) async fn setup() -> Result<()> {
    println!("Choose a setup mode:\n");
    println!("  1. app            create a Slack app; the bot reads what it is invited to");
    println!("  2. impersonation  use your own Slack session; slackie acts as you");
    let mode = ask("Mode [1 app / 2 impersonation]: ")?;
    match mode.as_str() {
        "1" => setup_app().await,
        "2" => setup_session().await,
        _ => Err(error_report("choose 1 or 2")),
    }
}
fn yesno(q: &str) -> Result<bool> {
    let a = ask(q)?;
    match a.to_lowercase().as_str() {
        "y" | "yes" => Ok(true),
        "n" | "no" => Ok(false),
        _ => Err(error_report("answer y or n")),
    }
}
async fn read_preferences() -> Result<Config> {
    let mode = ask("Read mode [1 mention-only / 2 all channel messages]: ")?;
    if mode != "1" && mode != "2" {
        return Err(error_report("choose 1 or 2"));
    }
    Ok(Config {
        read_app_mentions_only: mode == "1",
        read_dms: yesno("Allow DMs? [y/n]: ")?,
        read_private_channels: yesno("Allow private channels? [y/n]: ")?,
        read_message_groups: yesno("Allow message groups? [y/n]: ")?,
        ..Default::default()
    })
}
async fn setup_app() -> Result<()> {
    println!("\nCreate a Slack app from a manifest, then paste its tokens.\n");
    let name = ask("Bot name: ")?;
    let mut c = read_preferences().await?;
    println!(
        "\nCreate app at https://api.slack.com/apps → From an app manifest.\n{}",
        serde_json::to_string_pretty(&manifest(&name, &c))
            .map_err(Report::new)
            .change_context(AppError::Context("encode manifest"))?
    );
    println!("\nPaste the manifest into Slack and create the app. Install it to your workspace and approve requested permissions, then copy the Bot User OAuth Token.");
    let bot = ask("Bot User OAuth Token (xoxb-...): ")?;
    if !bot.starts_with("xoxb-") {
        return Err(error_report("invalid bot token: expected xoxb-"));
    }
    println!("In Basic Information → App-Level Tokens, create a token with connections:write.");
    let app = ask("App-level Socket Mode token (xapp-...): ")?;
    if !app.starts_with("xapp-") {
        return Err(error_report("invalid app token: expected xapp-"));
    }
    let slack = Slack::new(&bot)?;
    let auth: Auth = slack
        .call("auth.test", &[])
        .await
        .change_context(AppError::Context("verify bot token"))?;
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
async fn setup_session() -> Result<()> {
    println!(
        "\nImpersonation uses your own Slack session. From a browser where you are\nlogged into Slack, open DevTools → Application tab (Storage in Firefox) and\ncopy:\n  1. Local Storage → https://app.slack.com → localConfig_v2 → the xoxc-... value\n  2. Cookies → https://app.slack.com → the d cookie value (xoxd-...),\n     pasted exactly as shown, keeping any %XX escape sequences\nThey stay valid for months of normal Slack use; if slackie ever fails with\ninvalid_auth, extract fresh values and run slackie setup again.\n"
    );
    let token = ask("Session token (xoxc-...): ")?;
    if !token.starts_with("xoxc-") {
        return Err(error_report("invalid session token: expected xoxc-"));
    }
    let cookie = ask("d cookie (xoxd-...): ")?;
    if !cookie.starts_with("xoxd-") {
        return Err(error_report("invalid session cookie: expected xoxd-"));
    }
    let slack = Slack::with_cookie(&token, &cookie)?;
    let auth: Auth = slack
        .call("auth.test", &[])
        .await
        .change_context(AppError::Context("verify session token and cookie"))?;
    let mut c = read_preferences().await?;
    c.access_token = token.clone();
    c.session_token = token;
    c.session_cookie = cookie;
    c.bot_user_id = auth.user_id.clone();
    c.user_id = auth.user_id;
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

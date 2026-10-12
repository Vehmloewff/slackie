use crate::error::{error_report, AppError, Result};
use error_stack::{Report, ResultExt};
use serde::{Deserialize, Serialize};
use std::{
    env, fs,
    io::{self, Write},
    path::PathBuf,
};
#[derive(Clone, Debug, Default, Serialize, Deserialize)]
pub struct Config {
    #[serde(default)]
    pub access_token: String,
    #[serde(default)]
    pub bot_access_token: String,
    #[serde(default)]
    pub app_token: String,
    #[serde(default)]
    pub session_token: String,
    #[serde(default)]
    pub session_cookie: String,
    #[serde(default)]
    pub bot_user_id: String,
    #[serde(default)]
    pub user_id: String,
    #[serde(default)]
    pub team_id: String,
    #[serde(default)]
    pub team_name: String,
    #[serde(default)]
    pub last_seen: String,
    #[serde(default = "yes")]
    pub read_app_mentions_only: bool,
    #[serde(default = "yes")]
    pub read_dms: bool,
    #[serde(default = "yes")]
    pub read_private_channels: bool,
    #[serde(default = "yes")]
    pub read_message_groups: bool,
}
fn yes() -> bool {
    true
}
pub fn config_path() -> Result<PathBuf> {
    Ok(config_dir()?.join("config.json"))
}
pub fn config_dir() -> Result<PathBuf> {
    let base = env::var_os("XDG_CONFIG_HOME")
        .map(PathBuf::from)
        .or_else(|| env::var_os("HOME").map(|h| PathBuf::from(h).join(".config")))
        .ok_or_else(|| error_report("cannot resolve config directory"))?;
    Ok(base.join("slackie"))
}
/// Unix socket the slackie client uses to reach the slackied daemon.
pub fn socket_path() -> Result<PathBuf> {
    Ok(config_dir()?.join("slackie.sock"))
}
/// Directory for daemon state (message store, log file).
pub fn state_dir() -> Result<PathBuf> {
    let base = env::var_os("XDG_STATE_HOME")
        .map(PathBuf::from)
        .or_else(|| env::var_os("HOME").map(|h| PathBuf::from(h).join(".local").join("state")))
        .ok_or_else(|| error_report("cannot resolve state directory"))?;
    Ok(base.join("slackie"))
}
pub fn db_path() -> Result<PathBuf> {
    Ok(state_dir()?.join("store.db"))
}
pub fn log_path() -> Result<PathBuf> {
    Ok(state_dir()?.join("slackied.log"))
}
pub fn save_config(c: &Config) -> Result<()> {
    let p = config_path()?;
    fs::create_dir_all(p.parent().unwrap())
        .map_err(Report::new)
        .change_context(AppError::Context("create config directory"))?;
    let data = serde_json::to_vec_pretty(c)
        .map_err(Report::new)
        .change_context(AppError::Context("encode config"))?;
    let mut options = fs::OpenOptions::new();
    options.write(true).create(true).truncate(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.mode(0o600);
    }
    let mut file = options
        .open(&p)
        .map_err(Report::new)
        .change_context(AppError::Context("open config for writing"))?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        file.set_permissions(fs::Permissions::from_mode(0o600))
            .map_err(Report::new)
            .change_context(AppError::Context("secure config file"))?;
    }
    file.write_all(&data)
        .map_err(Report::new)
        .change_context(AppError::Context("write config"))?;
    Ok(())
}
pub fn load_config() -> Result<Config> {
    let p = config_path()?;
    let data = fs::read(&p)
        .map_err(Report::new)
        .attach_printable(format!("config path: {}", p.display()))
        .change_context(AppError::Context("read config"))?;
    let c: Config = serde_json::from_slice(&data)
        .map_err(Report::new)
        .change_context(AppError::Context("parse config"))?;
    if token(&c).is_empty() {
        return Err(error_report(
            "config has no access token; run: slackie setup",
        ));
    }
    Ok(c)
}
pub fn token(c: &Config) -> &str {
    if !c.bot_access_token.is_empty() {
        &c.bot_access_token
    } else {
        &c.access_token
    }
}
pub fn is_session(c: &Config) -> bool {
    !c.session_cookie.is_empty()
}
pub fn ask(prompt: &str) -> Result<String> {
    print!("{prompt}");
    io::stdout()
        .flush()
        .map_err(Report::new)
        .change_context(AppError::Context("flush prompt"))?;
    let mut s = String::new();
    io::stdin()
        .read_line(&mut s)
        .map_err(Report::new)
        .change_context(AppError::Context("read input"))?;
    let s = s.trim().to_owned();
    if s.is_empty() {
        return Err(error_report("input cannot be empty"));
    }
    Ok(s)
}

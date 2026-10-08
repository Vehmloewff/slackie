use err::{Err, Result, ResultExt};
use serde::{Deserialize, Serialize};
use std::{
    env, fs,
    io::{self, Write},
    path::PathBuf,
};
#[derive(Clone, Debug, Default, Serialize, Deserialize)]
pub(crate) struct Config {
    #[serde(default)]
    pub(crate) access_token: String,
    #[serde(default)]
    pub(crate) bot_access_token: String,
    #[serde(default)]
    pub(crate) app_token: String,
    #[serde(default)]
    pub(crate) bot_user_id: String,
    #[serde(default)]
    pub(crate) user_id: String,
    #[serde(default)]
    pub(crate) team_id: String,
    #[serde(default)]
    pub(crate) team_name: String,
    #[serde(default)]
    pub(crate) last_seen: String,
    #[serde(default = "yes")]
    pub(crate) read_app_mentions_only: bool,
    #[serde(default = "yes")]
    pub(crate) read_dms: bool,
    #[serde(default = "yes")]
    pub(crate) read_private_channels: bool,
    #[serde(default = "yes")]
    pub(crate) read_message_groups: bool,
}
fn yes() -> bool {
    true
}
pub(crate) fn config_path() -> Result<PathBuf> {
    let base = env::var_os("XDG_CONFIG_HOME")
        .map(PathBuf::from)
        .or_else(|| env::var_os("HOME").map(|h| PathBuf::from(h).join(".config")))
        .ok_or_else(|| Err::new("cannot resolve config directory"))?;
    Ok(base.join("slackie").join("config.json"))
}
pub(crate) fn save_config(c: &Config) -> Result<()> {
    let p = config_path()?;
    fs::create_dir_all(p.parent().unwrap()).wrap("create config directory")?;
    let data = serde_json::to_vec_pretty(c).wrap("encode config")?;
    let mut options = fs::OpenOptions::new();
    options.write(true).create(true).truncate(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.mode(0o600);
    }
    let mut file = options.open(&p).wrap("open config for writing")?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        file.set_permissions(fs::Permissions::from_mode(0o600))
            .wrap("secure config file")?;
    }
    file.write_all(&data).wrap("write config")?;
    Ok(())
}
pub(crate) fn load_config() -> Result<Config> {
    let p = config_path()?;
    let data =
        fs::read(&p).map_err(|e| Err::from_error(format!("read config {}: {e}", p.display())))?;
    let c: Config = serde_json::from_slice(&data).wrap("parse config")?;
    if token(&c).is_empty() {
        return Err(Err::new("config has no access token; run: slackie setup"));
    }
    Ok(c)
}
pub(crate) fn token(c: &Config) -> &str {
    if !c.bot_access_token.is_empty() {
        &c.bot_access_token
    } else {
        &c.access_token
    }
}
pub(crate) fn ask(prompt: &str) -> Result<String> {
    print!("{prompt}");
    io::stdout().flush().wrap("flush prompt")?;
    let mut s = String::new();
    io::stdin().read_line(&mut s).wrap("read input")?;
    let s = s.trim().to_owned();
    if s.is_empty() {
        return Err(Err::new("input cannot be empty"));
    }
    Ok(s)
}

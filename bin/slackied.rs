use std::fs::OpenOptions;
use std::io::{self, Write};

/// Log target that mirrors daemon logs to stderr and to the state
/// directory, so background-spawned daemons leave a trail.
struct Tee {
    file: Option<std::fs::File>,
}

impl Write for Tee {
    fn write(&mut self, buf: &[u8]) -> io::Result<usize> {
        let n = io::stderr().write(buf)?;
        if let Some(file) = self.file.as_mut() {
            let _ = file.write(buf);
        }
        Ok(n)
    }
    fn flush(&mut self) -> io::Result<()> {
        io::stderr().flush()?;
        if let Some(file) = self.file.as_mut() {
            let _ = file.flush();
        }
        Ok(())
    }
}

#[tokio::main]
async fn main() {
    // Error reports travel to clients over IPC as strings; keep them plain.
    error_stack::Report::set_color_mode(error_stack::fmt::ColorMode::None);
    let mut builder = env_logger::Builder::from_env(env_logger::Env::default().default_filter_or(
        if cfg!(debug_assertions) {
            "debug"
        } else {
            "info"
        },
    ));
    match slackie::config::log_path() {
        Ok(path) => {
            if let Some(parent) = path.parent() {
                let _ = std::fs::create_dir_all(parent);
            }
            match OpenOptions::new().create(true).append(true).open(&path) {
                Ok(file) => {
                    builder.target(env_logger::Target::Pipe(Box::new(Tee { file: Some(file) })));
                }
                Err(e) => eprintln!("slackied: cannot open log {}: {e}", path.display()),
            }
        }
        Err(e) => eprintln!("slackied: cannot resolve log path: {e:?}"),
    }
    builder.init();
    if let Err(e) = slackie::daemon::run().await {
        eprintln!("slackied error: {e:?}");
        std::process::exit(1);
    }
}

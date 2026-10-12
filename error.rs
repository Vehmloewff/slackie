use error_stack::Report;

#[derive(Debug, thiserror::Error)]
pub enum AppError {
    #[error("{0}")]
    Message(String),
    #[error("{0}")]
    Context(&'static str),
}

pub type Result<T> = error_stack::Result<T, AppError>;

pub fn error_report(value: impl Into<String>) -> Report<AppError> {
    Report::new(AppError::Message(value.into()))
}

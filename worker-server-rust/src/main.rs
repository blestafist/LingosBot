use clap::Parser;
use serde::Serialize;
use std::io::{self, Write};
use std::process::ExitCode;
use std::time::{Duration, Instant};
use worker_server_rust::config::{Cli, Config};
use worker_server_rust::error::{Error, Result};
use worker_server_rust::infrastructure::Session;
use worker_server_rust::workflow::{self, RunStats};

#[derive(Serialize)]
struct Success<'a> {
    success: bool,
    username: &'a str,
    words_learned: u64,
    duration_seconds: f64,
    message: &'a str,
}

#[derive(Serialize)]
struct Failure<'a> {
    success: bool,
    error_code: &'a str,
    message: &'a str,
}

fn write_json(value: &impl Serialize) -> io::Result<()> {
    let mut stdout = io::BufWriter::new(io::stdout().lock());
    serde_json::to_writer(&mut stdout, value)?;
    stdout.write_all(b"\n")?;
    stdout.flush()
}

fn failure(error: Error, json: bool) -> ExitCode {
    if json {
        if write_json(&Failure {
            success: false,
            error_code: error.code(),
            message: &error.message,
        })
        .is_err()
        {
            return ExitCode::from(2);
        }
    } else {
        eprintln!("{}: {}", error.code(), error.message);
    }
    ExitCode::from(error.exit_code())
}

fn run(config: &Config) -> Result<RunStats> {
    let mut session = Session::new(
        &config.base_url,
        Duration::from_secs(config.request_timeout_seconds),
        Duration::from_secs(config.max_run_seconds),
    )?;
    eprintln!("Logging in over HTTP...");
    let initial = session.login(
        &config.username,
        &config.password,
        &config.student_dashboard_url,
    )?;
    workflow::run(&mut session, config, initial)
}

fn main() -> ExitCode {
    let start = Instant::now();
    let args: Vec<_> = std::env::args_os().collect();
    let wants_json = args.iter().skip(1).any(|arg| arg == "--json");
    let cli = match Cli::try_parse_from(args) {
        Ok(cli) => cli,
        Err(error)
            if matches!(
                error.kind(),
                clap::error::ErrorKind::DisplayHelp | clap::error::ErrorKind::DisplayVersion
            ) =>
        {
            return if error.print().is_ok() {
                ExitCode::SUCCESS
            } else {
                ExitCode::from(2)
            };
        }
        Err(_) => {
            // clap diagnostics may quote a supplied password or environment value.
            return failure(
                Error::config("Invalid command-line arguments; use --help."),
                wants_json,
            );
        }
    };
    let json = cli.json;
    let config = match cli.resolve() {
        Ok(config) => config,
        Err(error) => return failure(error, json),
    };
    match run(&config) {
        Ok(stats) => {
            let message = if stats.lessons_completed > 0 {
                "Lesson completed successfully"
            } else if stats.classes_limited > 0 {
                "Daily lesson limit reached; no lessons completed"
            } else {
                "No lessons selected"
            };
            let result = if json {
                write_json(&Success {
                    success: true,
                    username: &config.username,
                    words_learned: stats.words_learned,
                    duration_seconds: start.elapsed().as_secs_f64(),
                    message,
                })
            } else {
                writeln!(
                    io::stdout().lock(),
                    "{message}: {} lesson(s), {} word(s), {:.2}s.",
                    stats.lessons_completed,
                    stats.words_learned,
                    start.elapsed().as_secs_f64()
                )
            };
            if result.is_ok() {
                ExitCode::SUCCESS
            } else {
                ExitCode::from(2)
            }
        }
        Err(error) => failure(error, json),
    }
}

use clap::{CommandFactory, Parser};
use serde::Serialize;
use serde_json::{Value, json};
use std::io::{self, Write};
use std::process::ExitCode;
use std::time::{Duration, Instant};
use worker_server_rust::config::{Cli, Config};
use worker_server_rust::error::{Error, Result};
use worker_server_rust::infrastructure::Session;
use worker_server_rust::workflow;

#[derive(Serialize)]
struct Success<'a> {
    success: bool,
    username: &'a str,
    words_learned: u64,
    lessons_completed: u64,
    errors_made: u64,
    api_calls: u64,
    classes_limited: u64,
    classes_empty: u64,
    challenge_snapshot: Option<workflow::RunChallengeSnapshot>,
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

fn failure(error: Error, json: bool, realtime: bool) -> ExitCode {
    if json {
        // Preserve the original failure even if its diagnostic cannot be written.
        let mut value = serde_json::to_value(Failure {
            success: false,
            error_code: error.code(),
            message: &error.message,
        })
        .expect("failure is serializable");
        if realtime {
            value["type"] = json!("result");
        }
        let _ = write_json(&value);
    } else {
        let _ = writeln!(io::stderr().lock(), "{}: {}", error.code(), error.message);
    }
    ExitCode::from(error.exit_code())
}

fn run(
    session: &mut Session,
    config: &Config,
    scrape_class: Option<i64>,
    list_classes: bool,
    scrape_all: bool,
) -> Result<Value> {
    let initial = session.login(
        &config.username,
        &config.password,
        &config.student_dashboard_url,
    )?;
    session.emit_event(&json!({"type":"login_success"}))?;
    if scrape_all {
        drop(initial);
        let scraped = workflow::scrape_all(session, config)?;
        return Ok(json!({"success":true, "classes":scraped.classes,
            "scrapes":scraped.scrapes, "api_calls":session.api_call_count()}));
    }
    if list_classes {
        let dashboard = workflow::parse_dashboard(&initial, None)?;
        return Ok(
            json!({"success":true, "classes": dashboard.classes, "api_calls": session.api_call_count()}),
        );
    }
    if let Some(group_id) = scrape_class {
        let scraped = workflow::scrape_class(session, config, group_id)?;
        return Ok(
            json!({"success": true, "scrape": scraped, "api_calls":session.api_call_count()}),
        );
    }
    let stats = workflow::run(session, config, initial)?;
    let message = if stats.lessons_completed > 0 {
        "Lesson completed successfully"
    } else if stats.classes_limited > 0 {
        "Daily lesson limit reached; no lessons completed"
    } else if stats.classes_empty > 0 {
        "No vocabulary available; no lessons completed"
    } else {
        "No lessons selected"
    };
    Ok(serde_json::to_value(Success {
        success: true,
        username: &config.username,
        words_learned: stats.words_learned,
        lessons_completed: stats.lessons_completed,
        errors_made: stats.errors_made,
        api_calls: session.api_call_count(),
        classes_limited: stats.classes_limited,
        classes_empty: stats.classes_empty,
        challenge_snapshot: stats.challenge_snapshot,
        duration_seconds: 0.0,
        message,
    })
    .expect("success is serializable"))
}

fn main() -> ExitCode {
    let start = Instant::now();
    let args: Vec<_> = std::env::args_os().collect();
    let cli = match Cli::try_parse_from(&args) {
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
            // Use only flags clap recognized before the first parse error. In
            // particular, a password of "--json" is not an output-format flag.
            let matches = Cli::command()
                .ignore_errors(true)
                .try_get_matches_from(args)
                .ok();
            let realtime = matches
                .as_ref()
                .and_then(|m| m.try_get_one::<bool>("realtime").ok().flatten())
                .copied()
                .unwrap_or(false);
            let wants_json = realtime
                || matches
                    .as_ref()
                    .and_then(|m| m.try_get_one::<bool>("json").ok().flatten())
                    .copied()
                    .unwrap_or(false)
                || matches
                    .as_ref()
                    .and_then(|m| m.try_get_one::<bool>("list_classes").ok().flatten())
                    .copied()
                    .unwrap_or(false)
                || matches
                    .as_ref()
                    .and_then(|m| m.try_get_one::<i64>("scrape_class").ok().flatten())
                    .is_some()
                || matches
                    .as_ref()
                    .and_then(|m| m.try_get_one::<bool>("scrape_all").ok().flatten())
                    .copied()
                    .unwrap_or(false);
            // clap diagnostics may quote a supplied password or environment value.
            return failure(
                Error::config("Invalid command-line arguments; use --help."),
                wants_json,
                realtime,
            );
        }
    };
    let realtime = cli.realtime;
    let json =
        cli.json || realtime || cli.scrape_class.is_some() || cli.list_classes || cli.scrape_all;
    let scrape_class = cli.scrape_class;
    let list_classes = cli.list_classes;
    let scrape_all = cli.scrape_all;
    let config = match cli.resolve() {
        Ok(config) => config,
        Err(error) => return failure(error, json, realtime),
    };
    let mut session = match Session::new(
        &config.base_url,
        Duration::from_secs(config.request_timeout_seconds),
        Duration::from_secs(config.max_run_seconds),
    ) {
        Ok(session) => session,
        Err(error) => return failure(error, json, realtime),
    };
    if realtime {
        session.enable_ndjson_stdout();
    }
    match run(
        &mut session,
        &config,
        scrape_class,
        list_classes,
        scrape_all,
    ) {
        Ok(mut value) => {
            value["duration_seconds"] = json!(start.elapsed().as_secs_f64());
            if realtime {
                value["type"] = json!("result");
            }
            let result = if json {
                write_json(&value)
            } else {
                writeln!(
                    io::stdout().lock(),
                    "{}: {} lesson(s), {} word(s), {:.2}s.",
                    value["message"].as_str().unwrap_or("Completed"),
                    value["lessons_completed"],
                    value["words_learned"],
                    start.elapsed().as_secs_f64()
                )
            };
            if result.is_ok() {
                ExitCode::SUCCESS
            } else {
                ExitCode::from(2)
            }
        }
        Err(error) => failure(error, json, realtime),
    }
}

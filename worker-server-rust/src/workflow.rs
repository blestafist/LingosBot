//! Sequential HTTP orchestration. Every selection and join is confirmed by a
//! fresh dashboard before a lesson can start; vocabulary is never guessed.

use std::collections::{BTreeMap, HashMap, HashSet};
use std::io::{self, Write};

use serde_json::{Value, json};

use crate::{
    challenge::{ChallengeSnapshot, confirm_challenge_join, normalize_text, parse_challenges},
    config::Config,
    error::{Error, Result},
    infrastructure::Session,
    lesson::run_lesson,
    models::LearningProps,
    vocabulary::Vocabulary,
};

// Bound allocation and work before constructing indices or scheduling requests.
const MAX_CLASSES: usize = 256;
const MAX_WORDSETS: usize = 256;
const MAX_WORDSET_CARDS: usize = 10_000;

#[derive(Clone, Copy, Debug, Default, Eq, PartialEq)]
pub struct RunStats {
    pub words_learned: u64,
    pub lessons_completed: u64,
    pub classes_limited: u64,
    pub classes_empty: u64,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ClassInfo {
    pub title: String,
    pub group_id: i64,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct Dashboard {
    pub current_group_id: i64,
    pub lesson_limit_reached: bool,
    pub uses_context_api: bool,
    pub classes: Vec<ClassInfo>,
    pub challenges: ChallengeSnapshot,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct WordsetInfo {
    pub id: i64,
    pub name: String,
    pub expected_count: Option<usize>,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct VocabularyEntry {
    pub word: String,
    pub translation: String,
}

pub fn resolve_count(
    default_count: u32,
    counts: &BTreeMap<String, u32>,
    class: &ClassInfo,
    discovered_classes: &[ClassInfo],
) -> u32 {
    if counts.is_empty() {
        return default_count;
    }
    let stable_key = format!("group:{}", class.group_id);
    if let Some((_, count)) = counts
        .iter()
        .find(|(key, _)| key.eq_ignore_ascii_case(&stable_key))
    {
        return *count;
    }
    // Legacy title settings may only apply when discovery makes them unique.
    if discovered_classes
        .iter()
        .filter(|candidate| titles_equal(&candidate.title, &class.title))
        .count()
        != 1
    {
        return default_count;
    }
    counts
        .iter()
        .find(|(title, _)| titles_equal(title, &class.title))
        .map_or(default_count, |(_, count)| *count)
}

fn resolve_counts(config: &Config, classes: &[ClassInfo]) -> Vec<u32> {
    if config.class_lesson_counts.is_empty() {
        return vec![config.lesson_count; classes.len()];
    }
    let overrides: HashMap<_, _> = config
        .class_lesson_counts
        .iter()
        .map(|(key, count)| (key.to_lowercase(), *count))
        .collect();
    let mut title_counts = HashMap::new();
    let titles: Vec<_> = classes
        .iter()
        .map(|class| class.title.to_lowercase())
        .collect();
    for title in &titles {
        *title_counts.entry(title.as_str()).or_insert(0_usize) += 1;
    }
    classes
        .iter()
        .zip(&titles)
        .map(|(class, title)| {
            overrides
                .get(&format!("group:{}", class.group_id))
                .or_else(|| {
                    (title_counts.get(title.as_str()) == Some(&1))
                        .then(|| overrides.get(title))
                        .flatten()
                })
                .copied()
                .unwrap_or(config.lesson_count)
        })
        .collect()
}

fn titles_equal(left: &str, right: &str) -> bool {
    left.to_lowercase() == right.to_lowercase()
}

/// Parse a dashboard's required nonempty group list without coercing IDs or
/// names. Duplicate titles are allowed; duplicate IDs are not.
pub fn parse_classes(dashboard: &Value) -> Result<Vec<ClassInfo>> {
    let groups = required(dashboard, "groups")?
        .as_array()
        .filter(|groups| !groups.is_empty())
        .ok_or_else(|| Error::parsing("Missing, empty or invalid dashboard groups."))?;
    if groups.len() > MAX_CLASSES {
        return Err(Error::parsing(
            "Dashboard exceeds the 256-class safety limit.",
        ));
    }
    let mut classes = Vec::with_capacity(groups.len());
    let mut ids = HashSet::with_capacity(groups.len());
    for group in groups {
        let id = positive_id(group, "id")?;
        if !ids.insert(id) {
            return Err(Error::parsing("Duplicate dashboard group ID."));
        }
        let title = nonempty_text(group, "name")?;
        classes.push(ClassInfo {
            title,
            group_id: id,
        });
    }
    Ok(classes)
}

/// Parse a complete inert page payload, including group membership and known
/// challenge states. Absent/null `contexts` selects the legacy endpoint;
/// a present field of another type is not a known dashboard.
pub fn parse_dashboard(page: &Value, expected_group: Option<i64>) -> Result<Dashboard> {
    let data = page_data(page, "dashboard", "dashboard")?;
    let current_group_id = positive_id(required(data, "currentGroup")?, "id")?;
    if expected_group.is_some_and(|expected| expected != current_group_id) {
        return Err(Error::parsing(
            "HTTP dashboard group selection was not confirmed.",
        ));
    }
    let lesson_limit_reached = required(data, "lessonLimitReached")?
        .as_bool()
        .ok_or_else(|| Error::parsing("Invalid dashboard lesson limit flag."))?;
    let classes = parse_classes(data)?;
    if !classes
        .iter()
        .any(|class| class.group_id == current_group_id)
    {
        return Err(Error::parsing(
            "Selected dashboard group is not in the group list.",
        ));
    }
    let uses_context_api = match data.get("contexts") {
        None | Some(Value::Null) => false,
        Some(Value::Object(_)) => true,
        Some(_) => return Err(Error::parsing("Invalid dashboard contexts.")),
    };
    let challenges = parse_challenges(data)?;
    Ok(Dashboard {
        current_group_id,
        lesson_limit_reached,
        uses_context_api,
        classes,
        challenges,
    })
}

pub fn group_switch_request(dashboard: &Dashboard, group_id: i64) -> Result<(&'static str, Value)> {
    if group_id <= 0
        || !dashboard
            .classes
            .iter()
            .any(|class| class.group_id == group_id)
    {
        return Err(Error::parsing("Cannot select an undiscovered HTTP group."));
    }
    Ok(if dashboard.uses_context_api {
        (
            "/student/context",
            json!({ "kind": "group", "id": group_id }),
        )
    } else {
        ("/student/group", json!({ "groupId": group_id }))
    })
}

/// Keep only small descriptors; the list page can be released before fetching
/// details. Missing optional counts are permitted, malformed present counts are
/// not. Every provided total must subsequently match the complete card array.
pub fn parse_wordsets(page: &Value, expected_group: i64) -> Result<Vec<WordsetInfo>> {
    let data = page_data(page, "wordsets", "wordsets")?;
    require_group(required(data, "currentGroup")?, expected_group)?;
    let sets = required(data, "wordsets")?
        .as_array()
        .ok_or_else(|| Error::parsing("Invalid HTTP wordset list."))?;
    if sets.len() > MAX_WORDSETS {
        return Err(Error::parsing(
            "Vocabulary exceeds the 256-wordset safety limit.",
        ));
    }
    let mut ids = HashSet::with_capacity(sets.len());
    let mut result = Vec::with_capacity(sets.len());
    for set in sets {
        let id = positive_id(set, "id")?;
        if !ids.insert(id) {
            return Err(Error::parsing("Duplicate HTTP wordset ID."));
        }
        result.push(WordsetInfo {
            id,
            name: nonempty_text(set, "name")?,
            expected_count: optional_total(set)?,
        });
    }
    Ok(result)
}

pub fn parse_wordset(
    page: &Value,
    expected_group: i64,
    expected_set: &WordsetInfo,
) -> Result<Vec<VocabularyEntry>> {
    let data = page_data(page, "wordset", "wordset")?;
    require_group(required(data, "group")?, expected_group)?;
    let metadata = required(data, "wordset")?;
    if positive_id(metadata, "id")? != expected_set.id {
        return Err(Error::parsing("Mismatched HTTP wordset ID."));
    }
    let cards = required(data, "flashcards")?
        .as_array()
        .ok_or_else(|| Error::parsing("Invalid HTTP flashcard array."))?;
    if cards.len() > MAX_WORDSET_CARDS {
        return Err(Error::parsing(
            "Wordset exceeds the 10000-card safety limit.",
        ));
    }
    for total in [
        expected_set.expected_count,
        optional_total(metadata)?,
        optional_total(data)?,
    ]
    .into_iter()
    .flatten()
    {
        if total != cards.len() {
            return Err(Error::parsing(
                "Incomplete HTTP vocabulary response; stopped without guessing.",
            ));
        }
    }
    let mut entries = Vec::with_capacity(cards.len());
    let mut ids = HashSet::new();
    for card in cards {
        // Card IDs are optional metadata; absent/null IDs need no validation.
        // A non-null ID must still be positive and unique within this set.
        if card.get("id").is_some_and(|id| !id.is_null()) && !ids.insert(positive_id(card, "id")?) {
            return Err(Error::parsing("Duplicate HTTP flashcard ID."));
        }
        entries.push(VocabularyEntry {
            word: nonempty_text(card, "word")?,
            translation: nonempty_text(card, "translation")?,
        });
    }
    Ok(entries)
}

/// The lesson engine owns exercise/transition validation. Check the page
/// envelope here and preserve the special daily-limit outcome before serde.
pub fn parse_learning_page(page: Value) -> Result<LearningProps> {
    match page.get("reason") {
        Some(Value::String(reason)) if reason == "limit-exceeded" => {
            return Err(Error::limit("Daily lesson limit reached."));
        }
        // Reason is nullable/optional in LearningProps, unlike the required
        // status, identifier and step of an ordinary learning page.
        None | Some(Value::Null | Value::String(_)) => {}
        Some(_) => return Err(Error::parsing("Invalid learning page reason.")),
    }
    if required(&page, "status")?.as_str() != Some("ok") {
        return Err(Error::parsing("Unrecognized learning page status."));
    }
    if required(&page, "lessonId")?
        .as_u64()
        .is_none_or(|id| id == 0)
    {
        return Err(Error::parsing("Invalid HTTP lesson identifier."));
    }
    if !required(&page, "step")?.is_object() {
        return Err(Error::parsing("Missing or invalid learning page step."));
    }
    serde_json::from_value(page)
        .map_err(|_| Error::parsing("Invalid learning page payload; stopped without guessing."))
}

pub fn run(
    session: &mut Session,
    config: &Config,
    initial_dashboard_page: Value,
) -> Result<RunStats> {
    let initial = parse_dashboard(&initial_dashboard_page, None)?;
    let classes = initial.classes.clone();
    // Do not retain an initial page (or its stale challenge state) across runs.
    drop(initial_dashboard_page);
    let mut stats = RunStats::default();
    let counts = resolve_counts(config, &classes);
    // login() just fetched and validated this dashboard. Reuse it only for
    // initial selection, never as a stale challenge snapshot after a lesson.
    let mut initial = Some(initial);

    // Include default counts, not only explicit overrides, in the run budget.
    counts.iter().try_fold(0_u32, |total, count| {
        total
            .checked_add(*count)
            .filter(|total| *total <= crate::config::MAX_LESSONS)
            .ok_or_else(|| {
                Error::config("Total lessons across discovered classes must not exceed 10000.")
            })
    })?;

    for (class, count) in classes.iter().zip(counts) {
        if count == 0 {
            continue;
        }
        let dashboard = match initial.take() {
            Some(dashboard) => dashboard,
            None => read_dashboard(session, config, None)?,
        };
        match run_class(session, config, class, count, &mut stats, dashboard) {
            Ok(()) => {}
            Err(error) if error.is_limit() => {
                stats.classes_limited += 1;
                // No server error text, prompts, response bodies or credentials.
                let _ = writeln!(
                    io::stderr().lock(),
                    "Daily lesson limit reached; skipping the remaining lessons for this class."
                );
            }
            Err(error) => return Err(error),
        }
    }
    // Zero completed lessons is valid when every class is disabled or limited.
    Ok(stats)
}

fn run_class(
    session: &mut Session,
    config: &Config,
    class: &ClassInfo,
    count: u32,
    stats: &mut RunStats,
    mut dashboard: Dashboard,
) -> Result<()> {
    let group_id = class.group_id;
    if dashboard.current_group_id != group_id {
        let (url, body) = group_switch_request(&dashboard, group_id)?;
        session.post(url, Some(&body))?;
        dashboard = read_dashboard(session, config, Some(group_id))?;
    }
    check_limit(&dashboard)?;
    let vocabulary = collect_vocabulary(session, group_id)?;
    if vocabulary.is_empty() {
        stats.classes_empty += 1;
        let _ = writeln!(
            io::stderr().lock(),
            "Class has no vocabulary; skipping its lessons."
        );
        return Ok(());
    }

    for _ in 0..count {
        // Always read afresh, including after lesson completion and joins.
        let dashboard = read_dashboard(session, config, Some(group_id))?;
        check_limit(&dashboard)?;
        let perfectionism = ensure_challenge(session, config, dashboard, group_id)?;
        let page = session.get_page(&format!("/learning/start/0?groupId={group_id}"))?;
        let initial = parse_learning_page(page)?;
        let lesson = run_lesson(session, config, &vocabulary, perfectionism, initial)?;
        stats.words_learned = stats
            .words_learned
            .checked_add(lesson.words_learned)
            .ok_or_else(|| Error::parsing("Learned word counter overflow."))?;
        stats.lessons_completed += 1;
    }
    Ok(())
}

fn read_dashboard(session: &Session, config: &Config, group: Option<i64>) -> Result<Dashboard> {
    let page = session.get_page(&config.student_dashboard_url)?;
    parse_dashboard(&page, group)
}

fn check_limit(dashboard: &Dashboard) -> Result<()> {
    if dashboard.lesson_limit_reached {
        Err(Error::limit("Daily lesson limit reached."))
    } else {
        Ok(())
    }
}

fn ensure_challenge(
    session: &Session,
    config: &Config,
    mut dashboard: Dashboard,
    group_id: i64,
) -> Result<bool> {
    if dashboard.challenges.active().is_none()
        && let Some(best) = dashboard.challenges.best_available()
    {
        let id = best.id;
        // The URL is constructed from a strictly validated positive numeric ID.
        session.post(&format!("/student/challenges/{id}"), None)?;
        dashboard = read_dashboard(session, config, Some(group_id))?;
        confirm_challenge_join(&dashboard.challenges, id)?;
        check_limit(&dashboard)?;
    }
    Ok(dashboard.challenges.active_perfectionism().is_some())
}

fn collect_vocabulary(session: &Session, group_id: i64) -> Result<Vocabulary> {
    let sets = parse_wordsets(&session.get_page("/student/wordsets")?, group_id)?;
    let mut vocabulary = Vocabulary::new();
    // Sequential small GETs preserve session cookie ordering. Release each
    // detail page before the next request; never cache all wordset HTML/props.
    for set in sets {
        let page = session.get_page(&format!("/student/wordsets/{}", set.id))?;
        let entries = parse_wordset(&page, group_id, &set)?;
        drop(page);
        for entry in entries {
            vocabulary.insert(&entry.word, &entry.translation)?;
        }
    }
    Ok(vocabulary)
}

fn page_data<'a>(page: &'a Value, page_name: &str, field: &str) -> Result<&'a Value> {
    if required(page, "page")?.as_str() != Some(page_name) {
        return Err(Error::parsing("Unrecognized or mismatched HTTP page."));
    }
    let data = required(page, field)?;
    if !data.is_object() {
        return Err(Error::parsing("Invalid HTTP page data."));
    }
    Ok(data)
}

fn required<'a>(object: &'a Value, key: &str) -> Result<&'a Value> {
    object
        .get(key)
        .ok_or_else(|| Error::parsing("Missing required HTTP data field."))
}

fn positive_id(object: &Value, field: &str) -> Result<i64> {
    required(object, field)?
        .as_i64()
        .filter(|id| *id > 0)
        .ok_or_else(|| Error::parsing("Invalid HTTP identifier."))
}

fn nonempty_text(object: &Value, field: &str) -> Result<String> {
    required(object, field)?
        .as_str()
        .map(normalize_text)
        .filter(|text| !text.is_empty())
        .ok_or_else(|| Error::parsing("Missing or empty HTTP text field."))
}

fn require_group(group: &Value, expected_group: i64) -> Result<()> {
    if positive_id(group, "id")? != expected_group {
        return Err(Error::parsing("Mismatched HTTP vocabulary group."));
    }
    Ok(())
}

fn optional_total(object: &Value) -> Result<Option<usize>> {
    let Some(counts) = object.get("counts") else {
        return Ok(None);
    };
    if !counts.is_object() {
        return Err(Error::parsing("Invalid HTTP vocabulary counts."));
    }
    let Some(total) = counts.get("total") else {
        return Ok(None);
    };
    total
        .as_u64()
        .and_then(|total| usize::try_from(total).ok())
        .map(Some)
        .ok_or_else(|| Error::parsing("Invalid HTTP vocabulary total count."))
}

#[cfg(test)]
#[path = "../tests/unit/workflow.rs"]
mod tests;

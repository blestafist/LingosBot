//! Typed learning API payloads. Required exercise/answer fields deliberately have
//! no serde defaults: an incomplete response must never cause another POST.

use serde::{Deserialize, Deserializer, Serialize, de};

#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct LearningProps {
    pub status: String,
    pub reason: Option<String>,
    pub lesson_id: u64,
    pub step: Option<LearningStep>,
}

impl<'de> Deserialize<'de> for LearningProps {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: Deserializer<'de>,
    {
        #[derive(Deserialize)]
        #[serde(rename_all = "camelCase")]
        struct Payload {
            #[serde(default)]
            status: String,
            #[serde(default)]
            reason: Option<String>,
            lesson_id: Option<u64>,
            #[serde(default)]
            step: Option<LearningStep>,
        }

        let payload = Payload::deserialize(deserializer)?;
        // Limit pages omit lessonId/step, but ordinary lesson pages must not
        // silently manufacture a missing identifier.
        let lesson_id = match payload.lesson_id {
            Some(id) => id,
            None if payload.reason.as_deref() == Some("limit-exceeded") => 0,
            None => return Err(de::Error::missing_field("lessonId")),
        };
        Ok(Self {
            status: payload.status,
            reason: payload.reason,
            lesson_id,
            step: payload.step,
        })
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct LearningStep {
    pub state: String,
    #[serde(default)]
    pub continue_url: Option<String>,
    #[serde(default)]
    pub exercise: Option<LearningExercise>,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct LearningExercise {
    #[serde(rename = "type")]
    pub kind: String,
    pub identifier: u64,
    pub main: String,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct AnswerRequest {
    pub identifier: u64,
    pub answer: String,
    pub skip: bool,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct AnswerResult {
    pub step: LearningStep,
    #[serde(default)]
    pub feedback: Option<LearningFeedback>,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct LearningFeedback {
    pub result: i32,
}

#[cfg(test)]
mod tests;

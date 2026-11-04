//! Typed learning API payloads. Required exercise/answer fields deliberately have
//! no serde defaults: an incomplete response must never cause another POST.

use serde::{Deserialize, Deserializer, Serialize, de};

// Do not buffer into a JSON Value: serde must still see duplicate fields.
fn deserialize_object<'de, D, T>(deserializer: D) -> Result<T, D::Error>
where
    D: Deserializer<'de>,
    T: Deserialize<'de>,
{
    struct ObjectVisitor<T>(std::marker::PhantomData<T>);

    impl<'de, T: Deserialize<'de>> de::Visitor<'de> for ObjectVisitor<T> {
        type Value = T;

        fn expecting(&self, formatter: &mut std::fmt::Formatter) -> std::fmt::Result {
            formatter.write_str("a JSON object")
        }

        fn visit_map<M: de::MapAccess<'de>>(self, map: M) -> Result<T, M::Error> {
            T::deserialize(de::value::MapAccessDeserializer::new(map))
        }
    }

    deserializer.deserialize_map(ObjectVisitor(std::marker::PhantomData))
}

// Declare fields once, retaining derived serialization and field validation.
// The private payload is only deserialized after checking the object boundary.
macro_rules! object_model {
    (
        $(#[$attr:meta])*
        pub struct $model:ident {
            $($(#[$field_attr:meta])* pub $field:ident: $field_type:ty),* $(,)?
        }
    ) => {
        #[derive(Debug, Clone, PartialEq, Eq, Serialize)]
        $(#[$attr])*
        pub struct $model {
            $($(#[$field_attr])* pub $field: $field_type),*
        }

        impl<'de> Deserialize<'de> for $model {
            fn deserialize<D: Deserializer<'de>>(deserializer: D) -> Result<Self, D::Error> {
                #[derive(Deserialize)]
                $(#[$attr])*
                struct Payload {
                    $($(#[$field_attr])* $field: $field_type),*
                }

                let payload: Payload = deserialize_object(deserializer)?;
                Ok(Self { $($field: payload.$field),* })
            }
        }
    };
}

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

        let payload: Payload = deserialize_object(deserializer)?;
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

object_model! {
    #[serde(rename_all = "camelCase")]
    pub struct LearningStep {
        pub state: String,
        #[serde(default)]
        pub continue_url: Option<String>,
        #[serde(default)]
        pub exercise: Option<LearningExercise>,
    }
}

object_model! {
    pub struct LearningExercise {
        #[serde(rename = "type")]
        pub kind: String,
        pub identifier: u64,
        pub main: String,
    }
}

object_model! {
    pub struct AnswerRequest {
        pub identifier: u64,
        pub answer: String,
        pub skip: bool,
    }
}

object_model! {
    pub struct AnswerResult {
        pub step: LearningStep,
        #[serde(default)]
        pub feedback: Option<LearningFeedback>,
    }
}

object_model! {
    pub struct LearningFeedback {
        pub result: i32,
    }
}

#[cfg(test)]
#[path = "../tests/unit/models.rs"]
mod tests;

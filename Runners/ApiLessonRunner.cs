namespace LingosBotApp;

// One sequential request per answer; the returned step is the source of truth.
internal sealed class ApiLessonRunner(
    ILessonApiClient client,
    AppConfig config,
    Func<string, IReadOnlyList<string>> resolveAnswers,
    Func<bool> makeError,
    Func<string, string> wrongAnswer,
    Action<string>? log = null)
{
    public void Run(int lessonNumber, LearningProps initial)
    {
        var props = Validate(initial);
        var step = props.Step!;
        long? identifier = null;
        var candidateIndex = 0;
        var errorConsidered = false;
        var requests = 0;
        var prompts = 0;
        var transitions = 0;

        while (true)
        {
            if (step.State == "finished")
            {
                WriteLog($"Lesson {lessonNumber} completed through API after {prompts} words and {requests} calls.");
                // React still displays the initial exercise. Reload the dashboard
                // so subsequent challenge checks/lessons see fresh server state.
                client.Navigate(config.StudentDashboardUrl);
                return;
            }

            if (step.State == "change_wordset")
            {
                if (++transitions > config.EffectiveLessonPromptSafetyCap || string.IsNullOrWhiteSpace(step.ContinueUrl))
                    throw new InvalidOperationException("Invalid or repeated API wordset transition.");
                client.Navigate(step.ContinueUrl);
                props = Validate(client.TryRead());
                step = props.Step!;
                identifier = null;
                continue;
            }

            if (step.State == "select_level")
                throw new InvalidOperationException("Choose your language level on lingos.pl before running lessons.");
            if (step.State != "exercise" || step.Exercise is not { Identifier: > 0 } exercise)
                throw new InvalidOperationException($"Unrecognized lesson API step '{step.State}'. Stopped safely.");
            if (requests >= (long)config.EffectiveLessonPromptSafetyCap * 4)
                throw new InvalidOperationException($"Lesson {lessonNumber} exceeded the API submission safety cap.");

            if (identifier != exercise.Identifier)
            {
                identifier = exercise.Identifier;
                candidateIndex = 0;
                errorConsidered = false;
                if (exercise.Type == "flashcard" && ++prompts > config.EffectiveLessonPromptSafetyCap)
                    throw new InvalidOperationException($"Lesson {lessonNumber} exceeded the prompt safety cap.");
            }

            var prompt = TextNormalizer.Normalize(exercise.Main);
            string answer;
            var intentionalError = false;
            switch (exercise.Type)
            {
                case "flashcard":
                    if (string.IsNullOrWhiteSpace(prompt))
                        throw new InvalidOperationException("The lesson API prompt was empty.");
                    var candidates = resolveAnswers(prompt);
                    if (candidates.Count == 0)
                        throw new InvalidOperationException($"No collected translation exists for '{prompt}'. Stopped without guessing.");
                    if (candidateIndex >= candidates.Count)
                        throw new InvalidOperationException($"All {candidates.Count} stored answers were rejected for '{prompt}'.");
                    answer = candidates[candidateIndex];
                    if (!errorConsidered)
                    {
                        errorConsidered = true;
                        intentionalError = makeError();
                        if (intentionalError) answer = wrongAnswer(answer);
                    }
                    break;
                case "new_teacher":
                case "new_premium":
                    answer = string.Empty;
                    break;
                default:
                    throw new InvalidOperationException($"Unrecognized lesson API exercise '{exercise.Type}'. Stopped without guessing.");
            }

            var wordLabel = exercise.Type == "flashcard" ? $"Word {prompts}" : "New word";
            WriteLog($"[Lesson {lessonNumber} · {wordLabel} · Call {++requests}] {prompt} -> {answer}" +
                (intentionalError ? " [intentional error]" : string.Empty));
            var result = client.Answer(props.LessonId, new LearningAnswerRequest(exercise.Identifier, answer));
            if (result.Step is null)
                throw new InvalidOperationException("Lesson API returned no next step.");
            if (exercise.Type == "flashcard" && result.Feedback is null)
                throw new InvalidOperationException("Lesson API returned no answer feedback. Stopped without resubmitting.");
            if (intentionalError && result.Feedback is { Result: > 0 })
                throw new InvalidOperationException("The intentional wrong answer was unexpectedly accepted.");
            if (!intentionalError && result.Feedback is { Result: <= 0 })
            {
                candidateIndex++;
                WriteLog($"Answer '{answer}' was rejected for '{prompt}'. Following the server's next step.");
            }
            step = result.Step;
        }
    }

    private void WriteLog(string message)
    {
        if (log is not null) log(message);
        else Console.WriteLine(message);
    }

    private static LearningProps Validate(LearningProps? props)
    {
        if (props?.Reason == "limit-exceeded")
            throw new LessonLimitReachedException("Lingos reported that today's lesson limit has been reached.");
        if (props is not { Status: "ok", LessonId: > 0, Step: not null })
            throw new InvalidOperationException("Could not read a valid lesson API state after navigation.");
        return props;
    }
}

namespace LingosBotApp;

internal sealed class LearningProps
{
    public string Status { get; set; } = string.Empty;
    public string Reason { get; set; } = string.Empty;
    public long LessonId { get; set; }
    public LearningStep? Step { get; set; }
}

internal sealed class LearningStep
{
    public string State { get; set; } = string.Empty;
    public string ContinueUrl { get; set; } = string.Empty;
    public LearningExercise? Exercise { get; set; }
}

internal sealed class LearningExercise
{
    public string Type { get; set; } = string.Empty;
    public long Identifier { get; set; }
    public string Main { get; set; } = string.Empty;
}

internal sealed class LearningAnswerResult
{
    public LearningStep? Step { get; set; }
    public LearningFeedback? Feedback { get; set; }
}

internal sealed class LearningFeedback
{
    public int Result { get; set; }
}

internal sealed record LearningAnswerRequest(long Identifier, string Answer, bool Skip = false);

namespace LingosBotApp;

internal static class LessonLimitDetector
{
    private const string LessonStartPath = "/learning/start";

    public static bool IsReached(string pageSource) =>
        !pageSource.Contains(LessonStartPath, StringComparison.OrdinalIgnoreCase);
}

namespace LingosBotApp;

internal static class ErrorRateResolver
{
    public static int GetEffectiveRate(int configuredRate, bool perfectionismChallengeActive) =>
        perfectionismChallengeActive ? 0 : configuredRate;
}

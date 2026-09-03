using Xunit;

namespace LingosBotApp.Tests;

public sealed class ChallengeTests
{
    [Theory]
    [InlineData("Perfekcjonizm", "Wykonaj 1 lekcję z maksymalnie 1 błędem", true)]
    [InlineData("  PERFEKCJONIZM  ", "Wykonaj   1 lekcje z MAKSYMALNIE 1 BŁĘDEM.", true)]
    [InlineData("Perfekcjonizm II", "Wykonaj 3 lekcje z maksymalnie jednym błędem", true)]
    [InlineData("Wyzwanie specjalne", "Perfekcjonizm: wykonaj 2 lekcje z maksymalnie 1 bledem", true)]
    [InlineData("Perfekcjonizm", "Wykonaj 1 lekcję — do zera błędów", true)]
    [InlineData("Perfekcjonizm", "Wykonaj 1 lekcję z maksymalnie dwóch błędów", false)]
    [InlineData("Perfekcjonizm", "Wykonaj 1 lekcję bez błędów", true)]
    [InlineData("Wyzwanie specjalne", "Perfekcjonizmu: wykonaj 1 lekcję bez błędów", true)]
    [InlineData("Perfekcyjna lekcja", "Wykonaj 1 lekcję z maksymalnie 1 błędem", false)]
    [InlineData("Wytrwałość", "To nie jest perfekcjonizm ani wyzwanie z błędami", false)]
    [InlineData("Wytrwałość", "Nie perfekcjonizm: wykonaj 1 lekcję z maksymalnie 1 błędem", false)]
    [InlineData("Perfekcjonizm", "Wykonaj 1 lekcję z maksymalnie 2 błędami", false)]
    public void PerfectionismMatchesSemanticVariants(string title, string description, bool expected)
    {
        var active = new ChallengeInfo(
            title,
            5,
            string.Empty,
            Completed: false,
            Description: description);

        Assert.True(active.IsActive);
        Assert.Equal(expected, active.IsPerfectionism);
    }

    [Theory]
    [InlineData("Perfekcjonizm", "Wykonaj 1 lekcję z maksymalnie 1 błędem", "/student/challenges/202", false, false)]
    [InlineData("Perfekcjonizm", "Wykonaj 1 lekcję z maksymalnie 1 błędem", "", true, false)]
    public void PerfectionismRequiresAnActiveChallenge(
        string title,
        string description,
        string joinUrl,
        bool completed,
        bool expected)
    {
        var challenge = new ChallengeInfo(title, 5, joinUrl, completed, description);

        Assert.Equal(!completed && string.IsNullOrWhiteSpace(joinUrl), challenge.IsActive);
        Assert.Equal(expected, challenge.IsPerfectionism);
    }

    [Fact]
    public void TitleKeywordRemainsSufficientWhenDescriptionIsMissing()
    {
        var active = new ChallengeInfo("Perfekcjonizm", 5, string.Empty, Completed: false);

        Assert.True(active.IsPerfectionism);
    }

    [Fact]
    public void DiacriticFreeAndNonBreakingWhitespaceAreEquivalent()
    {
        var active = new ChallengeInfo(
            "Perfekcjonizm",
            5,
            string.Empty,
            Completed: false,
            Description: "Wykonaj\u00a01 lekcje z maksymalnie 1 bledem");

        Assert.True(active.IsPerfectionism);
    }

    [Fact]
    public void ErrorRateResolverStillOnlyChangesRateForActivePerfectionism()
    {
        Assert.Equal(0, ErrorRateResolver.GetEffectiveRate(85, perfectionismChallengeActive: true));
        Assert.Equal(85, ErrorRateResolver.GetEffectiveRate(85, perfectionismChallengeActive: false));
    }

    [Fact]
    public void AvailableAndCompletedChallengesNeverDisableErrors()
    {
        var active = new ChallengeInfo(
            "Perfekcjonizm",
            5,
            string.Empty,
            Completed: false,
            Description: "Wykonaj 1 lekcję z maksymalnie 1 błędem");
        var available = active with { JoinUrl = "/student/challenges/202" };
        var completed = active with { Completed = true };

        Assert.False(available.IsActive);
        Assert.False(available.IsPerfectionism);
        Assert.False(completed.IsPerfectionism);
    }
}

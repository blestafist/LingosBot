using Xunit;

namespace LingosBotApp.Tests;

public sealed class ChallengeTests
{
    [Fact]
    public void PerfectionismMatchesOnlyTheOneLessonOneErrorChallenge()
    {
        var active = new ChallengeInfo(
            "Perfekcjonizm",
            5,
            string.Empty,
            Completed: false,
            Description: "Wykonaj 1 lekcję z maksymalnie 1 błędem");
        var available = active with { JoinUrl = "/student/challenges/202" };
        var otherPerfectionism = active with
        {
            Title = "Perfekcjonizm II",
            Description = "Wykonaj 3 lekcje z maksymalnie 1 błędem"
        };
        var wrongCondition = active with { Description = "Wykonaj 1 lekcję z maksymalnie 2 błędami" };
        var completed = active with { Completed = true };

        Assert.True(active.IsActive);
        Assert.True(active.IsPerfectionism);
        Assert.False(available.IsActive);
        Assert.False(available.IsPerfectionism);
        Assert.False(otherPerfectionism.IsPerfectionism);
        Assert.False(wrongCondition.IsPerfectionism);
        Assert.False(completed.IsPerfectionism);
    }

    [Fact]
    public void PerfectionismForcesZeroRateWithoutChangingConfiguredRateOtherwise()
    {
        Assert.Equal(0, ErrorRateResolver.GetEffectiveRate(85, perfectionismChallengeActive: true));
        Assert.Equal(85, ErrorRateResolver.GetEffectiveRate(85, perfectionismChallengeActive: false));
    }
}

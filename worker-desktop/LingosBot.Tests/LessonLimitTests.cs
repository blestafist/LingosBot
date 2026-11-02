using Xunit;

namespace LingosBotApp.Tests;

public sealed class LessonLimitTests
{
    [Fact]
    public void IsReachedReturnsFalseWhenDashboardContainsLessonStartLink()
    {
        const string pageSource = "<a href=\"https://lingos.pl/learning/start/0?groupId=26083\">Learn</a>";

        Assert.False(LessonLimitDetector.IsReached(pageSource));
    }

    [Fact]
    public void IsReachedReturnsTrueWhenDashboardDoesNotContainLessonStartLink()
    {
        const string pageSource = "<main>Daily lesson limit reached</main>";

        Assert.True(LessonLimitDetector.IsReached(pageSource));
    }
}

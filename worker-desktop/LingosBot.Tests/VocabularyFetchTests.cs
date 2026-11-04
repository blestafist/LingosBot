using Xunit;

namespace LingosBotApp.Tests;

public sealed class VocabularyFetchTests
{
    [Fact]
    public void ParsesParallelResponsesInOrderAndKeepsFailedPagesForFallback()
    {
        const string json = """
            [[{"foreignWord":"  Haus  ","polishTranslation":" dom "}],null,[]]
            """;

        var pages = VocabularyCollector.ParseFetchedWordsets(json, 3);

        Assert.NotNull(pages);
        Assert.Equal("Haus", Assert.Single(pages[0]!).ForeignWord);
        Assert.Equal("dom", Assert.Single(pages[0]!).PolishTranslation);
        Assert.Null(pages[1]);
        Assert.Empty(pages[2]!);
    }

    [Fact]
    public void MismatchedBatchFallsBackToNavigation()
    {
        Assert.Null(VocabularyCollector.ParseFetchedWordsets("[]", 2));
    }
}

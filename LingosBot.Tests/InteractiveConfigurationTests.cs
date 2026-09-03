using Xunit;

namespace LingosBotApp.Tests;

public sealed class InteractiveConfigurationTests
{
    [Fact]
    public void GlobalLessonCountDoesNotDiscoverClassesWhenSeparateConfigurationIsDeclined()
    {
        var config = CreateConfig();
        config.ClassLessonCounts["Existing class"] = 2;
        var discoveryCalls = 0;
        var input = new StringReader("no\n\n\n\n\n\n\n4\n");

        InteractiveConfiguration.Configure(
            config,
            input,
            TextWriter.Null,
            () =>
            {
                discoveryCalls++;
                throw new InvalidOperationException("Class discovery should not run.");
            });

        Assert.Equal(4, config.LessonCount);
        Assert.Equal(2, config.ClassLessonCounts["Existing class"]);
        Assert.Equal(0, discoveryCalls);
    }

    [Fact]
    public void SeparateConfigurationDiscoversClassesAndSavesEachCount()
    {
        var config = CreateConfig();
        var input = new StringReader("yes\n\n\n\n\n\n\n3\n2\n0\n");
        var classes = new[]
        {
            new ClassInfo("First class", "/first"),
            new ClassInfo("Second class", "/second")
        };

        InteractiveConfiguration.Configure(config, input, TextWriter.Null, () => classes);

        Assert.Equal(3, config.LessonCount);
        Assert.Equal(2, config.ClassLessonCounts["First class"]);
        Assert.Equal(0, config.ClassLessonCounts["Second class"]);
    }

    [Fact]
    public void SeparateConfigurationReusesExistingCountRegardlessOfClassNameCasing()
    {
        var config = CreateConfig();
        config.ClassLessonCounts = new Dictionary<string, int> { ["FIRST CLASS"] = 5 };
        var input = new StringReader("yes\n\n\n\n\n\n\n\n\n");
        var classes = new[] { new ClassInfo("First class", "/first") };

        InteractiveConfiguration.Configure(config, input, TextWriter.Null, () => classes);

        Assert.Equal(5, config.ClassLessonCounts["First class"]);
    }

    private static AppConfig CreateConfig() => new()
    {
        Credentials = new AppCredentials("student@example.com", "password")
    };
}

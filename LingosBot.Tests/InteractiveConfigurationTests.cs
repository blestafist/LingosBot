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
            new ClassInfo("First class", "/first", "101"),
            new ClassInfo("Second class", "/second", "202")
        };

        InteractiveConfiguration.Configure(config, input, TextWriter.Null, () => classes);

        Assert.Equal(3, config.LessonCount);
        Assert.Equal(2, config.ClassLessonCounts["group:101"]);
        Assert.Equal(0, config.ClassLessonCounts["group:202"]);
    }

    [Fact]
    public void SeparateConfigurationDoesNotMaterializeGlobalFallbackAsClassOverrides()
    {
        var config = CreateConfig();
        var input = new StringReader("yes\n\n\n\n\n\n\n3\n\n\n");
        var classes = new[]
        {
            new ClassInfo("First class", "/first", "101"),
            new ClassInfo("Second class", "/second", "202")
        };

        InteractiveConfiguration.Configure(config, input, TextWriter.Null, () => classes);

        Assert.Equal(3, config.LessonCount);
        Assert.Empty(config.ClassLessonCounts);
    }

    [Fact]
    public void SeparateConfigurationSavesOnlyExplicitOverrideAndUsesFallbackForBlankClass()
    {
        var config = CreateConfig();
        var input = new StringReader("yes\n\n\n\n\n\n\n3\n5\n\n");
        var classes = new[]
        {
            new ClassInfo("First class", "/first", "101"),
            new ClassInfo("Second class", "/second", "202")
        };

        InteractiveConfiguration.Configure(config, input, TextWriter.Null, () => classes);

        Assert.Equal(5, config.ClassLessonCounts["group:101"]);
        Assert.DoesNotContain("group:202", config.ClassLessonCounts.Keys);
        Assert.Equal(3, ClassLessonConfiguration.ResolveCount(config, classes[1], classes));
    }

    [Fact]
    public void SeparateConfigurationPreservesOverridesForUndiscoveredClassesAndLegacyKeys()
    {
        var config = CreateConfig();
        config.ClassLessonCounts = new Dictionary<string, int>
        {
            ["group:999"] = 8,
            ["Unseen old class"] = 6
        };
        var input = new StringReader("yes\n\n\n\n\n\n\n3\n\n");
        var classes = new[] { new ClassInfo("Visible class", "/visible", "101") };

        InteractiveConfiguration.Configure(config, input, TextWriter.Null, () => classes);

        Assert.Equal(8, config.ClassLessonCounts["group:999"]);
        Assert.Equal(6, config.ClassLessonCounts["Unseen old class"]);
        Assert.DoesNotContain("group:101", config.ClassLessonCounts.Keys);
    }

    [Fact]
    public void BlankInputPreservesExistingLegacyOverrideKey()
    {
        var config = CreateConfig();
        config.ClassLessonCounts = new Dictionary<string, int> { ["Visible class"] = 6 };
        var input = new StringReader("yes\n\n\n\n\n\n\n\n");
        var classes = new[] { new ClassInfo("Visible class", "/visible", "101") };

        InteractiveConfiguration.Configure(config, input, TextWriter.Null, () => classes);

        Assert.Equal(6, config.ClassLessonCounts["Visible class"]);
        Assert.DoesNotContain("group:101", config.ClassLessonCounts.Keys);
    }

    [Fact]
    public void FallbackKeywordRemovesExistingClassOverride()
    {
        var config = CreateConfig();
        config.LessonCount = 3;
        config.ClassLessonCounts = new Dictionary<string, int> { ["group:101"] = 7 };
        var input = new StringReader("yes\nstudent@example.com\npassword\nfalse\nChrome\n10\n\n3\ndefault\n");
        var classes = new[] { new ClassInfo("Visible class", "/visible", "101") };

        InteractiveConfiguration.Configure(config, input, TextWriter.Null, () => classes);

        Assert.DoesNotContain("group:101", config.ClassLessonCounts.Keys);
        Assert.Equal(3, ClassLessonConfiguration.ResolveCount(config, classes[0], classes));
    }

    [Fact]
    public void ClassPromptExplainsGlobalFallbackAndRemovalSyntax()
    {
        var config = CreateConfig();
        var output = new StringWriter();
        var input = new StringReader("yes\n\n\n\n\n\n\n\n");
        var classes = new[] { new ClassInfo("Visible class", "/visible", "101") };

        InteractiveConfiguration.Configure(config, input, output, () => classes);

        Assert.Contains("global fallback: 1", output.ToString());
        Assert.Contains("default/fallback/- removes override", output.ToString());
    }

    [Fact]
    public void SeparateConfigurationKeepsExistingOverrideButLeavesOtherClassesOnGlobalFallback()
    {
        var config = CreateConfig();
        config.LessonCount = 3;
        config.ClassLessonCounts = new Dictionary<string, int> { ["group:101"] = 5 };
        var input = new StringReader("yes\n\n\n\n\n\n\n\n\n");
        var classes = new[]
        {
            new ClassInfo("First class", "/first", "101"),
            new ClassInfo("Second class", "/second", "202")
        };

        InteractiveConfiguration.Configure(config, input, TextWriter.Null, () => classes);

        Assert.Equal(5, config.ClassLessonCounts["group:101"]);
        Assert.DoesNotContain("group:202", config.ClassLessonCounts.Keys);
        Assert.Equal(3, ClassLessonConfiguration.ResolveCount(config, classes[1], classes));
    }

    [Fact]
    public void MissingClassOverridesUseGlobalCountIndependentlyForEveryClass()
    {
        var config = CreateConfig();
        config.LessonCount = 4;
        var classes = new[]
        {
            new ClassInfo("First class", "/first", "101"),
            new ClassInfo("Second class", "/second", "202")
        };

        Assert.Equal(4, ClassLessonConfiguration.ResolveCount(config, classes[0], classes));
        Assert.Equal(4, ClassLessonConfiguration.ResolveCount(config, classes[1], classes));
    }

    [Fact]
    public void SeparateConfigurationReusesExistingCountRegardlessOfClassNameCasing()
    {
        var config = CreateConfig();
        config.ClassLessonCounts = new Dictionary<string, int> { ["FIRST CLASS"] = 5 };
        var input = new StringReader("yes\n\n\n\n\n\n\n\n\n\n");
        var classes = new[] { new ClassInfo("First class", "/first", "101") };

        InteractiveConfiguration.Configure(config, input, TextWriter.Null, () => classes);

        Assert.Equal(5, config.ClassLessonCounts["FIRST CLASS"]);
        Assert.DoesNotContain("group:101", config.ClassLessonCounts.Keys);
    }

    [Fact]
    public void SeparateConfigurationDistinguishesDuplicateTitlesByGroupId()
    {
        var config = CreateConfig();
        var output = new StringWriter();
        var input = new StringReader("yes\n\n\n\n\n\n\n\n4\n1\n");
        var classes = new[]
        {
            new ClassInfo("German", "/first", "101"),
            new ClassInfo("German", "/second", "202")
        };

        InteractiveConfiguration.Configure(config, input, output, () => classes);

        Assert.Equal(4, config.ClassLessonCounts["group:101"]);
        Assert.Equal(1, config.ClassLessonCounts["group:202"]);
        Assert.DoesNotContain("German'", output.ToString());
        Assert.Contains("German (ID 101)", output.ToString());
        Assert.Contains("German (ID 202)", output.ToString());
    }

    [Fact]
    public void SeparateConfigurationDistinguishesDuplicateTitlesByChangeUrlWhenGroupIdIsMissing()
    {
        var config = CreateConfig();
        var input = new StringReader("yes\n\n\n\n\n\n\n\n2\n6\n");
        var classes = new[]
        {
            new ClassInfo("German", "https://lingos.example/first"),
            new ClassInfo("gErMaN", "https://lingos.example/second")
        };

        InteractiveConfiguration.Configure(config, input, TextWriter.Null, () => classes);

        Assert.Equal(2, config.ClassLessonCounts["url:https://lingos.example/first"]);
        Assert.Equal(6, config.ClassLessonCounts["url:https://lingos.example/second"]);
    }

    [Fact]
    public void StableClassCountsRoundTripThroughJsonAndRuntimeSelection()
    {
        var configPath = Path.Combine(Path.GetTempPath(), $"lingosbot-config-{Guid.NewGuid():N}.json");
        try
        {
            var config = CreateConfig();
            config.ClassLessonCounts = new Dictionary<string, int>
            {
                ["group:101"] = 2,
                ["group:202"] = 7
            };
            AppConfig.Save(config, configPath);

            var loaded = AppConfig.Load(configPath, validate: false);
            var classes = new[]
            {
                new ClassInfo("Same title", "/first", "101"),
                new ClassInfo("Same title", "/second", "202")
            };

            Assert.Equal(2, ClassLessonConfiguration.ResolveCount(loaded, classes[0], classes));
            Assert.Equal(7, ClassLessonConfiguration.ResolveCount(loaded, classes[1], classes));
            var json = File.ReadAllText(configPath);
            Assert.Contains("group:101", json);
            Assert.Contains("group:202", json);
        }
        finally
        {
            File.Delete(configPath);
        }
    }

    [Fact]
    public void LegacyTitleEntryIsUsedOnlyWhenTitleIsUnique()
    {
        var uniqueConfig = CreateConfig();
        uniqueConfig.ClassLessonCounts = new Dictionary<string, int> { ["FIRST CLASS"] = 5 };
        var uniqueClass = new ClassInfo("First class", "/first", "101");

        Assert.Equal(5, ClassLessonConfiguration.ResolveCount(uniqueConfig, uniqueClass, new[] { uniqueClass }));

        var duplicateConfig = CreateConfig();
        duplicateConfig.ClassLessonCounts = new Dictionary<string, int> { ["German"] = 5 };
        var duplicateClasses = new[]
        {
            new ClassInfo("German", "/first", "101"),
            new ClassInfo("German", "/second", "202")
        };

        Assert.Equal(1, ClassLessonConfiguration.ResolveCount(duplicateConfig, duplicateClasses[0], duplicateClasses));
        Assert.Equal(1, ClassLessonConfiguration.ResolveCount(duplicateConfig, duplicateClasses[1], duplicateClasses));
    }

    private static AppConfig CreateConfig() => new()
    {
        Credentials = new AppCredentials("student@example.com", "password")
    };
}

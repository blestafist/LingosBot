using System.Text.Json;
using Xunit;

namespace LingosBotApp.Tests;

public sealed class RunModeTests
{
    [Theory]
    [InlineData("api", true)]
    [InlineData("api", false)]
    [InlineData("browser", true)]
    [InlineData("browser", false)]
    public void RunFlagsOnlyChangeHeadlessNotMode(string mode, bool headless)
    {
        var config = new AppConfig { Mode = mode };
        Program.ApplyRunOverrides(config, CommandLineOptions.Parse([headless ? "--headless" : "--visible"]));
        Assert.Equal(mode, config.Mode);
        Assert.Equal(headless, config.Headless);
    }

    [Fact]
    public void ModeAndHeadlessCanBeSetTogether()
    {
        var config = new AppConfig();
        Program.ApplyConfigurationChanges(config, CommandLineOptions.Parse(["--set_mode", "browser", "--set_headless", "true"]));
        Assert.Equal(RunMode.Browser, config.EffectiveMode);
        Assert.True(config.Headless);
        Program.ApplyRunOverrides(config, CommandLineOptions.Parse(["--mode", "api", "--visible"]));
        Assert.Equal(RunMode.Api, config.EffectiveMode);
        Assert.False(config.Headless);
    }

    [Theory]
    [InlineData("{}")]
    [InlineData("{\"Mode\":null,\"Headless\":true}")]
    [InlineData("{\"Mode\":\"\",\"Headless\":false}")]
    [InlineData("{\"Headless\":true}")]
    public void MissingModeAlwaysDefaultsToApi(string json)
    {
        Assert.Equal(RunMode.Api, JsonSerializer.Deserialize<AppConfig>(json)!.EffectiveMode);
    }

    [Theory]
    [InlineData(true)]
    [InlineData(false)]
    public void ApiIgnoresBrowserSettings(bool headless)
    {
        var config = new AppConfig
        {
            Credentials = new("test@example.org", "test"),
            Headless = headless, Browser = "unsupported", BrowserBinaryPath = "/missing",
            DefaultWaitTimeoutSeconds = -1, ShortWaitTimeoutSeconds = -1,
            PageLoadTimeoutSeconds = -1, PollingIntervalMilliseconds = -1,
            LessonRestartReuseTimeoutMilliseconds = -1, UseLessonApi = false
        };
        config.Validate();
        Assert.Equal(RunMode.Api, config.EffectiveMode);
        Assert.False(config.UseHttpAfterLogin);
        using var session = LingosHttpSession.FromCookies(config, new System.Net.CookieContainer());
        Assert.Throws<InvalidOperationException>(() => new BrowserFactory().Create(config));
    }

    [Theory]
    [InlineData("browser-headless")]
    [InlineData("typo")]
    public void InvalidModeFailsValidation(string mode)
    {
        var config = new AppConfig { Mode = mode, Credentials = new("test@example.org", "test") };
        Assert.Throws<InvalidOperationException>(config.Validate);
    }

    [Fact]
    public void BrowserSettingsAreValidatedOnlyInBrowserMode()
    {
        var config = new AppConfig { Mode = "browser", Browser = "unsupported", Credentials = new("test@example.org", "test") };
        Assert.Throws<InvalidOperationException>(config.Validate);
    }

    [Fact]
    public void InteractiveApiConfigurationSkipsBrowserPrompts()
    {
        var config = new AppConfig { Credentials = new("test@example.org", "test") };
        var output = new StringWriter();
        InteractiveConfiguration.Configure(config, new StringReader("no\n\n\n\n20\n3\n"), output,
            () => throw new Exception("Must not log in"));
        Assert.Equal(20, config.ErrorsPer100Words);
        Assert.Equal(3, config.LessonCount);
        Assert.DoesNotContain("Headless", output.ToString());
        Assert.DoesNotContain("Browser", output.ToString());
    }
}

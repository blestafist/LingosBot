using OpenQA.Selenium;
using OpenQA.Selenium.Chrome;
using OpenQA.Selenium.Edge;
using OpenQA.Selenium.Firefox;
using OpenQA.Selenium.Safari;

namespace LingosBotApp;

internal sealed class BrowserFactory
{
    public IWebDriver Create(AppConfig config)
    {
        IWebDriver driver = config.Browser.ToLowerInvariant() switch
        {
            "firefox" => CreateFirefox(config),
            "edge" => CreateEdge(config),
            "safari" => CreateSafari(config),
            "chrome" or _ => CreateChrome(config)
        };

        driver.Manage().Timeouts().PageLoad = config.PageLoadTimeout;
        driver.Manage().Timeouts().ImplicitWait = TimeSpan.Zero;

        return driver;
    }

    private static IWebDriver CreateChrome(AppConfig config)
    {
        var service = ChromeDriverService.CreateDefaultService();
        service.HideCommandPromptWindow = true;

        var options = new ChromeOptions();

        if (config.Headless)
        {
            options.AddArgument("--headless=new");
        }

        options.AddArgument("--mute-audio");
        options.AddArgument("--start-maximized");
        options.AddArgument("--window-size=1600,1000");
        options.AddArgument("--no-first-run");
        options.AddArgument("--disable-default-apps");
        options.AddArgument("--disable-background-networking");
        options.AddArgument("--disable-background-timer-throttling");
        options.AddArgument("--disable-backgrounding-occluded-windows");
        options.AddArgument("--disable-component-update");
        options.AddArgument("--disable-extensions");
        options.AddArgument("--disable-features=Translate,OptimizationHints,MediaRouter,ChromeWhatsNewUI,AutofillServerCommunication");
        options.AddArgument("--disable-notifications");
        options.AddArgument("--disable-popup-blocking");
        options.AddArgument("--disable-renderer-backgrounding");
        options.AddArgument("--disable-search-engine-choice-screen");
        options.AddArgument("--disable-sync");
        options.AddArgument("--metrics-recording-only");
        options.AddArgument("--lang=pl-PL");
        options.AddUserProfilePreference("profile.default_content_setting_values.images", 2);
        options.AddUserProfilePreference("credentials_enable_service", false);
        options.AddUserProfilePreference("profile.password_manager_enabled", false);
        options.PageLoadStrategy = PageLoadStrategy.Eager;

        if (!string.IsNullOrWhiteSpace(config.ChromeBinaryPath))
        {
            options.BinaryLocation = config.ChromeBinaryPath;
            Console.WriteLine($"Using Chrome binary from LINGOS_CHROME_BINARY: {config.ChromeBinaryPath}");
        }

        return new ChromeDriver(service, options);
    }

    private static IWebDriver CreateFirefox(AppConfig config)
    {
        var options = new FirefoxOptions();

        if (config.Headless)
        {
            options.AddArgument("-headless");
        }

        options.SetPreference("media.volume_scale", "0.0");
        options.SetPreference("media.default_volume", "0.0");
        options.SetPreference("permissions.default.microphone", 2);
        options.SetPreference("permissions.default.camera", 2);
        options.SetPreference("permissions.default.desktop-notification", 2);
        options.PageLoadStrategy = PageLoadStrategy.Eager;

        return new FirefoxDriver(options);
    }

    private static IWebDriver CreateEdge(AppConfig config)
    {
        var options = new EdgeOptions();

        if (config.Headless)
        {
            options.AddArgument("--headless=new");
        }

        options.AddArgument("--mute-audio");
        options.AddArgument("--log-level=3");
        options.AddArgument("--no-sandbox");
        options.AddArgument("--disable-dev-shm-usage");
        options.PageLoadStrategy = PageLoadStrategy.Eager;

        return new EdgeDriver(options);
    }

    private static IWebDriver CreateSafari(AppConfig config)
    {
        return new SafariDriver();
    }
}

using OpenQA.Selenium;
using OpenQA.Selenium.Chrome;
using OpenQA.Selenium.Edge;
using OpenQA.Selenium.Firefox;
using OpenQA.Selenium.Safari;

namespace LingosBot;

internal sealed class BrowserFactory
{
    public IWebDriver Create(Config config)
    {
        IWebDriver driver = config.browser.ToLower() switch
        {
            "firefox" => CreateFirefox(config.headless),
            "edge" => CreateEdge(config.headless),
            "safari" => CreateSafari(),
            "chrome" or _ => CreateChrome(config.headless)
        };

        driver.Manage().Timeouts().PageLoad = TimeSpan.FromSeconds(60);
        driver.Manage().Timeouts().ImplicitWait = TimeSpan.Zero;

        return driver;
    }

    private static IWebDriver CreateChrome(bool headless)
    {
        var service = ChromeDriverService.CreateDefaultService();
        service.HideCommandPromptWindow = true;

        var options = new ChromeOptions();

        if (headless)
        {
            options.AddArgument("--headless=new");
        }

        options.AddArgument("--mute-audio");
        options.AddArgument("--start-maximized");
        options.AddArgument("--window-size=1600,1000");
        options.AddArgument("--no-sandbox");
        options.AddArgument("--disable-dev-shm-usage");
        options.AddArgument("--log-level=3");
        options.AddArgument("--no-first-run");
        options.AddArgument("--disable-default-apps");
        options.AddArgument("--disable-background-networking");
        options.AddArgument("--disable-extensions");
        options.AddArgument("--disable-notifications");
        options.AddArgument("--disable-popup-blocking");
        options.AddArgument("--disable-sync");
        options.AddArgument("--lang=pl-PL");

        options.AddUserProfilePreference("profile.default_content_setting_values.images", 2);
        options.AddUserProfilePreference("credentials_enable_service", false);
        options.AddUserProfilePreference("profile.password_manager_enabled", false);
        options.PageLoadStrategy = PageLoadStrategy.Eager;

        return new ChromeDriver(service, options);
    }

    private static IWebDriver CreateFirefox(bool headless)
    {
        var options = new FirefoxOptions();

        if (headless)
        {
            options.AddArgument("-headless");
        }

        options.SetPreference("media.volume_scale", "0.0");
        options.SetPreference("media.default_volume", "0.0");
        options.SetPreference("permissions.default.microphone", 2);
        options.SetPreference("permissions.default.camera", 2);
        options.SetPreference("permissions.default.desktop-notification", 2);

        return new FirefoxDriver(options);
    }

    private static IWebDriver CreateEdge(bool headless)
    {
        var options = new EdgeOptions();

        if (headless)
        {
            options.AddArgument("--headless=new");
        }

        options.AddArgument("--mute-audio");
        options.AddArgument("--log-level=3");
        options.AddArgument("--no-sandbox");

        return new EdgeDriver(options);
    }

    private static IWebDriver CreateSafari()
    {
        return new SafariDriver();
    }
}

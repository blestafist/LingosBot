using OpenQA.Selenium;
using SeleniumExtras.WaitHelpers;
using OpenQA.Selenium.Support.UI;
using OpenQA.Selenium.Chrome;
using OpenQA.Selenium.Edge;
using OpenQA.Selenium.Firefox;
using OpenQA.Selenium.Safari;
using OpenQA.Selenium.Chromium;

namespace LingosBot
{
    public static class Helpers
    {
        public static IWebElement WaitForElement(By by, short timeout = 10, Func<IWebDriver, IWebElement>? function = null) // wait for element with set timeout and function (ExpectedConditions)
        {
            WebDriverWait wait = new(Bot.webDriver, TimeSpan.FromSeconds(timeout));
            if (function != null)
            {
                return wait.Until(function);
            }

            return wait.Until(ExpectedConditions.ElementIsVisible(by));
        }

        public static IWebDriver GetWebDriver() // just a switch case expression that returns correct web driver for your browser
        {
            return Enum.Parse<AvailibleDrivers>(Bot.config.browser) switch
            {
                AvailibleDrivers.Chrome => new ChromeDriver(BrowsersOptions.GetChromeOptions()),
                AvailibleDrivers.Edge => new EdgeDriver(BrowsersOptions.GetEdgeOptions()),
                AvailibleDrivers.Firefox => new FirefoxDriver(BrowsersOptions.GetFirefoxOptions()),
                AvailibleDrivers.Safari => new SafariDriver(),                
                _ => new ChromeDriver(BrowsersOptions.GetChromeOptions()),
            };
        }

        public static void ClickEnter()
        {
            try
            {
                var enterButton = WaitForElement(By.Id("enterBtn"), 5, ExpectedConditions.ElementToBeClickable(By.Id("enterBtn")));  // click enter button func (with JS)
                ((IJavaScriptExecutor)Bot.webDriver).ExecuteScript("arguments[0].click()", enterButton); // clicking
            }

            catch (Exception e) // handle exc
            {
                Console.WriteLine("Error while clicking enter: " + e.Message);
            }
        }
    }

    internal static class BrowsersOptions
    {
        public static ChromeOptions GetChromeOptions()
        {
            ChromeOptions options = new();
            
            options.AddArgument("--mute-audio");
            options.AddArgument("--autoplay-policy=no-user-gesture-required");

            options.AddArgument("--no-sandbox");
            options.AddArgument("--disable-dev-shm-usage");
            options.AddArgument("--log-level=3");

            return options;
        }

        public static EdgeOptions GetEdgeOptions()
        {
            EdgeOptions options = new();

            options.AddArgument("--mute-audio");
            options.AddArgument("--log-level=3");
            options.AddArgument("--no-sandbox");

            return options;
        }

        public static FirefoxOptions GetFirefoxOptions()
        {
            FirefoxOptions options = new();

            options.SetPreference("media.volume_scale", "0.0");
            options.SetPreference("media.default_volume", "0.0");
            
            // Blocking permissions
            options.SetPreference("permissions.default.microphone", 2);
            options.SetPreference("permissions.default.camera", 2);
            options.SetPreference("permissions.default.desktop-notification", 2);

            return options;
        }
    }
    
    enum AvailibleDrivers : byte // availible drivers (Browsers)
    {
        Firefox,
        Edge,
        Chrome,
        Safari
    }
}
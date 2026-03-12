using OpenQA.Selenium;
using SeleniumExtras.WaitHelpers;
using OpenQA.Selenium.Support.UI;
using OpenQA.Selenium.Chrome;
using OpenQA.Selenium.Edge;
using OpenQA.Selenium.Firefox;
using OpenQA.Selenium.Safari;
using OpenQA.Selenium.Chromium;
using OpenQA.Selenium.DevTools.V138.Cast;

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

        // error streak state to create groups (~2 errors in a row)
        private static int _errorStreakRemaining = 0;
        private static readonly int _streakTarget = 2; // target streak length (will vary +-2)

        public static bool MakeAnError()
        {
            // Determine average streak length for uniform [min,max]
            int minLen = Math.Max(1, _streakTarget - 2);
            int maxLen = _streakTarget + 2;
            double avgLen = (minLen + maxLen) / 2.0;

            double pTarget = Bot.config.errorsPer100Words / 100.0;
            double pStart = pTarget / avgLen; // per-word probability to start a streak

            if (_errorStreakRemaining > 0)
            {
                _errorStreakRemaining--;
                return true;
            }

            // decide whether to start a new streak
            double sample = Bot.rnd.NextDouble();
            if (sample < pStart)
            {
                _errorStreakRemaining = Bot.rnd.Next(minLen, maxLen + 1) - 1; // -1 because we'll consume one now
                return true;
            }

            return false;
        }

        public static string MakeTypo(string text)
        {
            if (string.IsNullOrEmpty(text)) return text;

            char RandomLetter()
            {
                const string letters = "abcdefghijklmnopqrstuvwxyz";
                return letters[Bot.rnd.Next(letters.Length)];
            }

            var sb = new System.Text.StringBuilder(text);
            int len = sb.Length;
            // choose typo operation
            int op = Bot.rnd.Next(0, 4); // 0-delete,1-swap,2-substitute,3-insert

            if (len == 1) op = Bot.rnd.Next(2,4); // avoid swap/delete for 1-char

            switch (op)
            {
                case 0: // delete random char (avoid deleting spaces)
                    {
                        int i = Bot.rnd.Next(0, len);
                        int tries = 0;
                        while (tries < 5 && char.IsWhiteSpace(sb[i])) { i = Bot.rnd.Next(0, len); tries++; }
                        if (!char.IsWhiteSpace(sb[i])) sb.Remove(i, 1);
                        break;
                    }
                case 1: // swap adjacent
                    {
                        int i = Bot.rnd.Next(0, Math.Max(1, len - 1));
                        char tmp = sb[i];
                        sb[i] = sb[i + 1];
                        sb[i + 1] = tmp;
                        break;
                    }
                case 2: // substitute
                    {
                        int i = Bot.rnd.Next(0, len);
                        if (!char.IsWhiteSpace(sb[i]))
                        {
                            char repl = RandomLetter();
                            if (char.IsUpper(sb[i])) repl = char.ToUpper(repl);
                            sb[i] = repl;
                        }
                        break;
                    }
                case 3: // insert
                    {
                        int i = Bot.rnd.Next(0, len + 1);
                        sb.Insert(i, RandomLetter());
                        break;
                    }
            }

            return sb.ToString();
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

            if (Bot.config.headless) { options.AddArgument("--headless"); }


            return options;
        }

        public static EdgeOptions GetEdgeOptions()
        {
            // EdgeOptions must indicate Chromium to accept Chrome-style arguments
            EdgeOptions options = new();

            // Use Selenium 4 Headless property
            if (Bot.config.headless) { options.AddArgument("--headless=new"); }
            options.AddArgument("--mute-audio");
            options.AddArgument("--log-level=3");
            options.AddArgument("--no-sandbox");

            return options;
        }

        public static FirefoxOptions GetFirefoxOptions()
        {
            FirefoxOptions options = new();

            // Run Firefox in headless mode
            if (Bot.config.headless) { options.AddArgument("-headless"); }

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
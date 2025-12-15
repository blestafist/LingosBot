using OpenQA.Selenium;
using SeleniumExtras.WaitHelpers;


namespace LingosBot
{

    internal class Bot
    {
        public static Config config = ConfigDataBaseTweaks.GetConfig(); // getting user config
        public static IWebDriver webDriver = Helpers.GetWebDriver();
        public static void Main(string[] args)  // program entry point
        {
            webDriver.Navigate().GoToUrl("https://lingos.pl/h/login"); // go to lingos url
            SeleniumMethods.Login();  // logging in

            for (int i = 0; i < config.numberOfLessons; i++)
            {
                SeleniumMethods.LaunchLesson();
                SeleniumMethods.DoLesson();
            }

            Console.ReadLine();
        }
    }



    internal static class SeleniumMethods // a static class for selenium methods, such as Login(), DoLesson() and others
    {
        
        public static void Login() // login to Lingos
        {
            if (Bot.config.automaticLogin)
            {
                try
                {
                    string login = Bot.config.email;
                    string password = Bot.config.password;

                    var declineCookies = Helpers.WaitForElement(By.Id("CybotCookiebotDialogBodyButtonDecline"), 15, ExpectedConditions.ElementToBeClickable(By.Id("CybotCookiebotDialogBodyButtonDecline")));
                    declineCookies.Click();

                    var loginBox = Helpers.WaitForElement(By.Name("login"), 10, ExpectedConditions.ElementToBeClickable(By.Name("login")));
                    var passwordBox = Bot.webDriver.FindElement(By.Name("password"));
                    var submitButton = Bot.webDriver.FindElement(By.Id("submit-login-button"));

                    loginBox.SendKeys(login);
                    passwordBox.SendKeys(password);
                    ((IJavaScriptExecutor)Bot.webDriver).ExecuteScript("arguments[0].click();", submitButton);
                }

                catch (Exception e)
                {
                    Console.WriteLine("Error while logging in: " + e.Message);
                }
            }

            else
            {
                Console.WriteLine("Login to Lingos and press enter...");
                Console.ReadLine();
            }
        }

        public static void LaunchLesson()
        {
            try
            {
                var launchLessonButton = Helpers.WaitForElement(By.PartialLinkText("UCZ SIĘ"));  // find element by part. name
                ((IJavaScriptExecutor)Bot.webDriver).ExecuteScript("arguments[0].click()", launchLessonButton); // clicking with JavaScript
            }

            catch (Exception e)
            {
                Console.WriteLine("Error while starting lesson. Message: " + e.Message);
            }
        }
        
        public static void DoLesson()
        {
            while (true)
            {
                if (Bot.webDriver.PageSource.Contains("UCZ SIĘ")) { return; }
                else if (Bot.webDriver.PageSource.Contains("Przetłumacz:"))
                {
                    // do translation
                }

                else if (Bot.webDriver.PageSource.Contains("Nowe słowo"))
                {
                    // write new word in DB
                }
            }
        }
    }

    
}
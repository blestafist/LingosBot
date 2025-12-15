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
                    string login = Bot.config.email; // reading config login
                    string password = Bot.config.password;  // and password

                    var declineCookies = Helpers.WaitForElement(By.Id("CybotCookiebotDialogBodyButtonDecline"), 15, ExpectedConditions.ElementToBeClickable(By.Id("CybotCookiebotDialogBodyButtonDecline")));
                    declineCookies.Click(); // decline fucking cookies

                    var loginBox = Helpers.WaitForElement(By.Name("login"), 10, ExpectedConditions.ElementToBeClickable(By.Name("login")));
                    var passwordBox = Bot.webDriver.FindElement(By.Name("password"));
                    var submitButton = Bot.webDriver.FindElement(By.Id("submit-login-button")); // find elemets of login

                    loginBox.SendKeys(login); // enter login
                    passwordBox.SendKeys(password); // enter password
                    ((IJavaScriptExecutor)Bot.webDriver).ExecuteScript("arguments[0].click();", submitButton); // click button via JS (element to be clickable)
                }

                catch (Exception e) // exc
                {
                    Console.WriteLine("Error while logging in: " + e.Message);
                }
            }

            else // if user wants no auto login
            {
                Console.WriteLine("Login to Lingos and press enter...");
                Console.ReadLine();
            }
        }

        public static void LaunchLesson() // launch lesson
        {
            try
            {
                var launchLessonButton = Helpers.WaitForElement(By.PartialLinkText("UCZ SIĘ"));  // find element by part. name (button UCZ SIE)
                ((IJavaScriptExecutor)Bot.webDriver).ExecuteScript("arguments[0].click()", launchLessonButton); // clicking with JavaScript
            }

            catch (Exception e) // handling exc
            {
                Console.WriteLine("Error while starting lesson. Message: " + e.Message);
            }
        }
        
        public static void DoLesson()
        {
            while (true)
            {
                if (Bot.webDriver.PageSource.Contains("UCZ SIĘ")) { return; } // if page contains UCZ SIE, end lesson
                else if (Bot.webDriver.PageSource.Contains("Przetłumacz:")) // not completed yet
                {
                    var wordToTranslate = Helpers.WaitForElement(By.Id("flashcard_main_text")).Text;
                    var inputField = Helpers.WaitForElement(By.Id("flashcard_answer_input"), 15, ExpectedConditions.ElementToBeClickable(By.Id("flashcard_answer_input")));

                    inputField.SendKeys("test");
                    Helpers.ClickEnter();
                }

                else if (Bot.webDriver.PageSource.Contains("Nowe słowo"))
                {
                    // write new word in DB
                }
            }
        }
    }

    
}
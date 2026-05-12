using OpenQA.Selenium;
using SeleniumExtras.WaitHelpers;


namespace LingosBot
{

    internal class Bot
    {
        public static Config config = ConfigDataBaseTweaks.GetConfig(); // getting user config
        public static WordsDataBaseTweaks dataBase = new();
        public static IWebDriver webDriver = Helpers.GetWebDriver();
        public static Random rnd = new(); // random generator for making errors with chance


        public static void Main(string[] args)  // program entry point
        {
            if (OperatingSystem.IsWindows()) { Console.WriteLine("INSTALL LINUX!"); }
            else if (OperatingSystem.IsLinux()) { Console.WriteLine("LINUX USER :)))"); }


            webDriver.Navigate().GoToUrl("https://lingos.pl/h/login"); // go to lingos url
            SeleniumMethods.Login();  // logging in
            dataBase.InitDB(); // Initialising words DB

            // Get all classes and iterate through them
            var classes = SeleniumMethods.GetAllClasses();
            Console.WriteLine($"Found {classes.Count} classes");

            foreach (var className in classes)
            {
                Console.WriteLine($"\n=== Working on class: {className} ===");
                SeleniumMethods.SwitchToClass(className);

                // Try to activate Wyzwanie if available
                SeleniumMethods.TryActivateWyzwanie();

                // Do lessons for this class
                for (int i = 0; i < config.numberOfLessons; i++)
                {
                    Console.WriteLine($"Lesson {i + 1}/{config.numberOfLessons} for class {className}");
                    SeleniumMethods.LaunchLesson();
                    SeleniumMethods.DoLesson();
                }
            }

            webDriver.Quit();
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

        public static List<string> GetAllClasses()
        {
            List<string> classes = new List<string>();

            try
            {
                // Wait for main page to load
                Helpers.WaitForElement(By.PartialLinkText("UCZ SIĘ"), 15);

                // Try to find class name on the page
                var classElements = Bot.webDriver.FindElements(By.CssSelector("h5.h5.mb-0"));

                foreach (var element in classElements)
                {
                    string className = element.Text.Trim();
                    if (!string.IsNullOrEmpty(className) && !classes.Contains(className))
                    {
                        classes.Add(className);
                        Console.WriteLine($"Found class: {className}");
                    }
                }

                // If no classes found, add a default one
                if (classes.Count == 0)
                {
                    classes.Add("default");
                    Console.WriteLine("No classes found, using default");
                }
            }
            catch (Exception e)
            {
                Console.WriteLine("Error while getting classes: " + e.Message);
                classes.Add("default");
            }

            return classes;
        }

        public static void SwitchToClass(string className)
        {
            try
            {
                // Navigate back to main page
                Bot.webDriver.Navigate().GoToUrl("https://lingos.pl/student-confirmed/group");
                Thread.Sleep(1000);

                Console.WriteLine($"Switched to class: {className}");
            }
            catch (Exception e)
            {
                Console.WriteLine("Error while switching class: " + e.Message);
            }
        }

        public static void TryActivateWyzwanie()
        {
            try
            {
                // Check if Wyzwania button exists and is clickable
                var wyzwaniaButton = Bot.webDriver.FindElements(By.CssSelector("a[data-bs-target='#wyzwaniaModal']"));

                if (wyzwaniaButton.Count > 0 && wyzwaniaButton[0].Displayed)
                {
                    Console.WriteLine("Wyzwanie available, trying to activate...");
                    ((IJavaScriptExecutor)Bot.webDriver).ExecuteScript("arguments[0].click();", wyzwaniaButton[0]);
                    Thread.Sleep(1000);

                    // Try to find and click first challenge in the modal
                    var challengeButtons = Bot.webDriver.FindElements(By.CssSelector("#wyzwaniaModal .btn-primary, #wyzwaniaModal button[type='submit']"));

                    if (challengeButtons.Count > 0)
                    {
                        ((IJavaScriptExecutor)Bot.webDriver).ExecuteScript("arguments[0].click();", challengeButtons[0]);
                        Console.WriteLine("Wyzwanie activated!");
                        Thread.Sleep(1000);
                    }
                    else
                    {
                        Console.WriteLine("No challenges found in modal");
                        // Close modal
                        var closeButton = Bot.webDriver.FindElements(By.CssSelector("#wyzwaniaModal .btn-close"));
                        if (closeButton.Count > 0)
                        {
                            closeButton[0].Click();
                        }
                    }
                }
                else
                {
                    Console.WriteLine("No Wyzwanie available");
                }
            }
            catch (Exception e)
            {
                Console.WriteLine("Error while activating Wyzwanie: " + e.Message);
            }
        }

        public static void LaunchLesson() // launch lesson
        {
            try
            {
                var launchLessonButton = Helpers.WaitForElement(By.PartialLinkText("UCZ SIĘ"));  // find element by part. name (button UCZ SIE)
                ((IJavaScriptExecutor)Bot.webDriver).ExecuteScript("arguments[0].click()", launchLessonButton); // clicking with JavaScript

                Helpers.WaitForElement(By.Id("flashcard_main_text"));
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
                if (Bot.webDriver.PageSource.Contains("Lekcja wykonana")) { return; } // if page contains UCZ SIE, end lesson
                else if (Bot.webDriver.PageSource.Contains("Przetłumacz")) // not completed yet
                {
                    Console.WriteLine("2nd");
                    var wordToTranslate = Helpers.WaitForElement(By.Id("flashcard_main_text")).Text;
                    var inputField = Helpers.WaitForElement(By.Id("flashcard_answer_input"), 15, ExpectedConditions.ElementToBeClickable(By.Id("flashcard_answer_input")));

                    if (Bot.dataBase.ExistsInDatabase(wordToTranslate))
                    {
                        if (!Helpers.MakeAnError())
                        {
                            inputField.SendKeys(Bot.dataBase.ReturnRandomTranslation(wordToTranslate));
                        }

                        Helpers.ClickEnter();

                        // Check if answer was correct or wrong
                        Thread.Sleep(500); // wait for page to update
                        bool isCorrect = Bot.webDriver.PageSource.Contains("btn-primary"); // success has btn-primary
                        bool isWrong = Bot.webDriver.PageSource.Contains("btn-danger"); // fail has btn-danger

                        if (isWrong)
                        {
                            // We got it wrong, save the correct answer
                            var correctWord = Helpers.WaitForElement(By.Id("flashcard_error_correct"), 10).Text;
                            Console.WriteLine($"Wrong answer! Correct word is: {correctWord}");
                            Bot.dataBase.WriteToDB(wordToTranslate, correctWord);
                        }
                        else if (isCorrect)
                        {
                            Console.WriteLine("Correct answer!");
                        }

                        Helpers.ClickEnter();
                    }

                    else
                    {
                        Helpers.ClickEnter();
                        var correctWord = Helpers.WaitForElement(By.Id("flashcard_error_correct"), 10).Text;
                        Console.WriteLine("I see the correct word, it is " + correctWord);
                        Bot.dataBase.WriteToDB(wordToTranslate, correctWord);
                        Helpers.ClickEnter();
                    }
                }

                else if (Bot.webDriver.PageSource.Contains("Nowe słowo"))
                {
                    Helpers.ClickEnter(); // just skip new word screen
                }
            }
        }
    }

    
}

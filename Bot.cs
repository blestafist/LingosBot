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

            // Get all classes from dropdown
            var classUrls = SeleniumMethods.GetAllClassUrls();
            Console.WriteLine($"Found {classUrls.Count} classes");

            foreach (var classUrl in classUrls)
            {
                Console.WriteLine($"\n=== Working on class: {classUrl.Key} ===");

                // Navigate to class
                webDriver.Navigate().GoToUrl("https://lingos.pl" + classUrl.Value);
                Thread.Sleep(1500);

                // Try to activate Wyzwanie if available
                SeleniumMethods.TryActivateWyzwanie();

                // Do lessons for this class
                for (int i = 0; i < config.numberOfLessons; i++)
                {
                    Console.WriteLine($"Lesson {i + 1}/{config.numberOfLessons} for class {classUrl.Key}");
                    SeleniumMethods.LaunchLesson();
                    SeleniumMethods.DoLesson();

                    // Return to class page after lesson
                    webDriver.Navigate().GoToUrl("https://lingos.pl" + classUrl.Value);
                    Thread.Sleep(1000);
                }
            }

            Console.WriteLine("\n=== All lessons completed for all classes! ===");
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

        public static Dictionary<string, string> GetAllClassUrls()
        {
            Dictionary<string, string> classUrls = new Dictionary<string, string>();

            try
            {
                // Navigate to main page
                Bot.webDriver.Navigate().GoToUrl("https://lingos.pl/student-confirmed/group");
                Thread.Sleep(1500);

                // Find all option elements with group-change URLs
                var options = Bot.webDriver.FindElements(By.CssSelector("option[value*='group-change']"));

                if (options.Count == 0)
                {
                    // Try to find select element first
                    var selects = Bot.webDriver.FindElements(By.TagName("select"));
                    foreach (var select in selects)
                    {
                        var selectOptions = select.FindElements(By.TagName("option"));
                        foreach (var option in selectOptions)
                        {
                            string value = option.GetAttribute("value");
                            if (!string.IsNullOrEmpty(value) && value.Contains("group-change"))
                            {
                                string className = option.Text.Trim();
                                if (!string.IsNullOrEmpty(className))
                                {
                                    classUrls[className] = value;
                                    Console.WriteLine($"Found class: {className} -> {value}");
                                }
                            }
                        }
                    }
                }
                else
                {
                    foreach (var option in options)
                    {
                        string value = option.GetAttribute("value");
                        string className = option.Text.Trim();

                        if (!string.IsNullOrEmpty(value) && !string.IsNullOrEmpty(className))
                        {
                            classUrls[className] = value;
                            Console.WriteLine($"Found class: {className} -> {value}");
                        }
                    }
                }

                // If no classes found, use current page
                if (classUrls.Count == 0)
                {
                    Console.WriteLine("No class dropdown found, using current page");
                    classUrls["default"] = "/student-confirmed/group";
                }
            }
            catch (Exception e)
            {
                Console.WriteLine("Error while getting class URLs: " + e.Message);
                classUrls["default"] = "/student-confirmed/group";
            }

            return classUrls;
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
                // Check if lesson is finished
                if (Bot.webDriver.Url.Contains("/group/finished", StringComparison.OrdinalIgnoreCase))
                {
                    Console.WriteLine("Lesson completed (URL check)!");
                    return;
                }

                if (Bot.webDriver.PageSource.Contains("UCZ SIĘ"))
                {
                    Console.WriteLine("Lesson completed!");
                    return;
                }

                // Wait for main text element
                IWebElement? mainEl = null;
                try
                {
                    mainEl = Helpers.WaitForElement(By.Id("flashcard_main_text"), 5);
                }
                catch
                {
                    Console.WriteLine("No main text found, retrying...");
                    Thread.Sleep(500);
                    continue;
                }

                string wordToTranslate = mainEl.Text.Trim();
                Console.WriteLine($"Question: {wordToTranslate}");

                // Check if input field is visible and enabled
                var inputElements = Bot.webDriver.FindElements(By.Id("flashcard_answer_input"));
                IWebElement? inputField = Helpers.FirstVisibleEnabled(inputElements);
                bool canType = inputField != null;

                if (canType)
                {
                    Console.WriteLine("Input field is available, entering answer...");

                    string answer = "";
                    if (Bot.dataBase.ExistsInDatabase(wordToTranslate))
                    {
                        if (!Helpers.MakeAnError())
                        {
                            answer = Bot.dataBase.ReturnRandomTranslation(wordToTranslate);
                            Console.WriteLine($"Using translation from DB: {answer}");
                        }
                        else
                        {
                            Console.WriteLine("Making intentional error (leaving blank)");
                        }
                    }
                    else
                    {
                        Console.WriteLine("Word not in database, leaving blank");
                    }

                    // Use FastSetInputValue instead of SendKeys
                    Helpers.FastSetInputValue(inputField!, answer);
                }
                else
                {
                    Console.WriteLine("No input field (probably 'Nowe słowo' or result screen)");
                }

                // Click Enter
                Helpers.ClickEnter();

                // Wait for state to change
                string currentWord = wordToTranslate;
                bool currentCanType = canType;

                try
                {
                    Helpers.WaitForCondition(() =>
                    {
                        // Check if finished
                        if (Bot.webDriver.Url.Contains("/group/finished", StringComparison.OrdinalIgnoreCase))
                        {
                            return true;
                        }

                        if (Bot.webDriver.PageSource.Contains("Lekcja wykonana"))
                        {
                            return true;
                        }

                        // Check if word changed
                        try
                        {
                            string nowText = Bot.webDriver.FindElement(By.Id("flashcard_main_text")).Text.Trim();
                            if (nowText != currentWord)
                            {
                                return true;
                            }
                        }
                        catch
                        {
                            // element not found, state changed
                            return true;
                        }

                        // Check if input availability changed
                        var nowInputs = Bot.webDriver.FindElements(By.Id("flashcard_answer_input"));
                        bool nowCanType = Helpers.FirstVisibleEnabled(nowInputs) != null;
                        if (nowCanType != currentCanType)
                        {
                            return true;
                        }

                        return false;
                    }, timeoutSeconds: 2.0, pollMs: 50);
                }
                catch (WebDriverTimeoutException)
                {
                    Console.WriteLine("State didn't change, retrying click...");
                    Helpers.ClickEnter();

                    try
                    {
                        Helpers.WaitForCondition(() =>
                        {
                            if (Bot.webDriver.Url.Contains("/group/finished", StringComparison.OrdinalIgnoreCase))
                            {
                                return true;
                            }

                            try
                            {
                                string nowText = Bot.webDriver.FindElement(By.Id("flashcard_main_text")).Text.Trim();
                                return nowText != currentWord;
                            }
                            catch
                            {
                                return true;
                            }
                        }, timeoutSeconds: 2.0, pollMs: 50);
                    }
                    catch
                    {
                        Console.WriteLine("Still stuck, continuing anyway...");
                    }
                }

                // If we just answered and now see the result, save correct answer if wrong
                if (canType && Bot.webDriver.PageSource.Contains("flashcard_error_correct"))
                {
                    try
                    {
                        var correctElement = Bot.webDriver.FindElement(By.Id("flashcard_error_correct"));
                        string correctWord = correctElement.Text.Trim();

                        if (Bot.webDriver.PageSource.Contains("btn-danger"))
                        {
                            Console.WriteLine($"Wrong answer! Correct word is: {correctWord}");
                            Bot.dataBase.WriteToDB(wordToTranslate, correctWord);
                        }
                        else
                        {
                            Console.WriteLine($"Correct answer: {correctWord}");
                        }
                    }
                    catch
                    {
                        // couldn't get correct word, ignore
                    }
                }
            }
        }
    }

    
}

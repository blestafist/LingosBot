using OpenQA.Selenium;
using SeleniumExtras.WaitHelpers;

namespace LingosBot
{
    internal class DataSetScraper
    {
        private IWebDriver driver;

        public DataSetScraper(IWebDriver webDriver)
        {
            driver = webDriver;
        }

        // Get all word set IDs from the main page
        public List<string> GetWordSetIds()
        {
            try
            {
                driver.Navigate().GoToUrl("https://lingos.pl/student-confirmed/wordsets");

                var setLinks = Helpers.WaitForElement(By.CssSelector("a[href*='/student-confirmed/wordset/']"), 15);
                var allLinks = driver.FindElements(By.CssSelector("a[href*='/student-confirmed/wordset/']"));

                List<string> setIds = new();

                foreach (var link in allLinks)
                {
                    string? href = link.GetAttribute("href");
                    if (href == null) continue;

                    string setId = href.Split('/').Last();

                    if (!setIds.Contains(setId))
                    {
                        setIds.Add(setId);
                    }
                }

                Console.WriteLine($"Found {setIds.Count} word sets");
                return setIds;
            }

            catch (Exception e)
            {
                Console.WriteLine("Error while getting word set IDs: " + e.Message);
                return new List<string>();
            }
        }

        // Scrape a single word set by ID
        public Dictionary<string, List<string>> ScrapeWordSet(string setId)
        {
            try
            {
                driver.Navigate().GoToUrl($"https://lingos.pl/student-confirmed/wordset/{setId}");

                Helpers.WaitForElement(By.CssSelector("div.flashcard-border-end"), 15);

                var leftSideElements = driver.FindElements(By.CssSelector("div.flashcard-border-end"));
                var rightSideElements = driver.FindElements(By.CssSelector("div.flashcard-border-start"));

                Dictionary<string, List<string>> wordPairs = new();

                for (int i = 0; i < leftSideElements.Count; i++)
                {
                    string key = leftSideElements[i].Text.Trim();
                    string value = rightSideElements[i].Text.Trim();

                    if (!string.IsNullOrEmpty(key) && !string.IsNullOrEmpty(value))
                    {
                        if (wordPairs.ContainsKey(key))
                        {
                            wordPairs[key].Add(value);
                        }
                        else
                        {
                            wordPairs[key] = new List<string> { value };
                        }
                    }
                }

                Console.WriteLine($"Scraped {wordPairs.Count} word pairs from set {setId}");
                return wordPairs;
            }

            catch (Exception e)
            {
                Console.WriteLine($"Error while scraping word set {setId}: " + e.Message);
                return new Dictionary<string, List<string>>();
            }
        }

        // Scrape all word sets and merge them into one dictionary
        public Dictionary<string, List<string>> ScrapeAllWordSets()
        {
            Dictionary<string, List<string>> allWords = new();

            List<string> setIds = GetWordSetIds();

            foreach (string setId in setIds)
            {
                Console.WriteLine($"Scraping set {setId}...");
                var wordPairs = ScrapeWordSet(setId);

                foreach (var pair in wordPairs)
                {
                    if (allWords.ContainsKey(pair.Key))
                    {
                        foreach (var value in pair.Value)
                        {
                            if (!allWords[pair.Key].Contains(value))
                            {
                                allWords[pair.Key].Add(value);
                            }
                        }
                    }
                    else
                    {
                        allWords[pair.Key] = new List<string>(pair.Value);
                    }
                }
            }

            Console.WriteLine($"Total words scraped: {allWords.Count}");
            return allWords;
        }
    }
}
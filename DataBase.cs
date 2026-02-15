using Newtonsoft.Json;
using OpenQA.Selenium.Support.Events;

namespace LingosBot
{

    public class Config
    {
        public bool automaticLogin = true;
        public string email = "";
        public string password = "";
        public int numberOfLessons = 3; // number of lessons to do
        public string browser = "Chrome";

        public string wordsDataBasePath = "words.json";
    }

    public class WordsDataBase
    {
        public Dictionary<string, string> words = []; // key is an original word, value is a translated word
    }

    public class WordsDataBaseTweaks 
    {
        private WordsDataBase dataBase = new();
        public void InitDB()
        {
            if (!File.Exists(Bot.config.wordsDataBasePath))
            {
                Console.WriteLine("Could not find database path, creating a new file");

                WordsDataBase empty = new();
                File.WriteAllText(Bot.config.wordsDataBasePath, JsonConvert.SerializeObject(empty));
            }

            dataBase = JsonConvert.DeserializeObject<WordsDataBase>(File.ReadAllText(Bot.config.wordsDataBasePath)) ?? new WordsDataBase();
        }
        public bool ExistsInDatabase(string word)
        {
            return dataBase.words.ContainsKey(word);
        }

        public string ReturnTranslation(string key)
        {
            return dataBase.words[key];
        }

        public void WriteToDB(string key, string value)
        {
            dataBase.words.Add(key, value);
            File.WriteAllText(Bot.config.wordsDataBasePath, JsonConvert.SerializeObject(dataBase, Formatting.Indented));
        }
    }
    
    public static class ConfigDataBaseTweaks
    {
        private const string filePath = "config.json";
        public static Config GetConfig()
        {

            if (!File.Exists(filePath)) {
                // File.WriteAllText(filePath, JsonConvert.SerializeObject(new Config(), Formatting.Indented))
                ConfigMode(); // Entering the configuration mode
            }
            
            Config config = JsonConvert.DeserializeObject<Config>(File.ReadAllText(filePath)) ?? new Config();
            return config ?? new Config();
        }

        private static void ConfigMode()
        {
            Config tempCfg = new();

            Console.Clear(); // Resetting the style and clearing hte buffer

            Console.ForegroundColor = ConsoleColor.Red;
            Console.WriteLine("No file named config.json, entering the config mode...");
            Console.ResetColor();

            Console.WriteLine("============================= CONFIGURATION =============================");
            Console.WriteLine("\nWelcome to configurator. Now you need to enter values for the bot to correctly do the tasks!");
            Console.WriteLine("You can change the values later in the config.json file, by opening it in any text editor");
            Console.WriteLine("To accept default config values, just click enter\n\n");

            Console.WriteLine("Do you want to enable the automatic login (y/n)? Default → yes: ");
            string input = Console.ReadLine() ?? "y"; input.Trim();
            if (input == "n" || input == "0") { tempCfg.automaticLogin = false; }

            Console.WriteLine("\nEnter your lingos.pl email: ");
            tempCfg.email = Console.ReadLine() ?? "WRONG EMAIL";

            Console.WriteLine("\nEnter your lingos.pl password: ");
            tempCfg.password = Console.ReadLine() ?? "WRONG PASSWORD";

            Console.WriteLine("\nEnter the amount of lessons to to in one bot session. Default → 3: ");
            input = Console.ReadLine() ?? "3"; input.Trim();

            if (input != "" || input != "3") { tempCfg.numberOfLessons = Convert.ToInt32(input); }

            Console.WriteLine("\nNow choose a browser you use from the list. \n1 → Chrome, \n2 → Microsoft Edge, \n3 → Firefox (Recommended, Default), \n4 → Safari (low support): ");
            input = Console.ReadLine() ?? ""; input.Trim();

            tempCfg.browser = input switch
            {
                "1" => "Chrome",
                "2" => "Edge",
                "3" => "Firefox",
                "4" => "Safari",
                _ => "Firefox"
            };

            File.WriteAllText(filePath, JsonConvert.SerializeObject(tempCfg, Formatting.Indented));

            Console.WriteLine("Config written. Please re-check the values in the config.json file!");

            Console.WriteLine("\n\n=========================================================================");

            Environment.Exit(0); // quit the program
        }
    }
}
using System;
using System.Collections.Generic;
using System.ComponentModel.DataAnnotations;
using System.Linq;
using System.Threading.Tasks;

namespace LingosBot
{

    public class ConfigDataBase
    {
        public string email = "ENTER YOUR LINGOS EMAIL HERE";
        public string password = "ENTER YOUR LINGOS PASSWORD HERE";
        public int numberOfLessons = 1; // number of lessons to do
    }
    
    public class WordsDataBase
    {
        public Dictionary<string, string> words = []; // key is an original word, value is a translated word
    }
}
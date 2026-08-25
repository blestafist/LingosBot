using OpenQA.Selenium;
using OpenQA.Selenium.Support.UI;

namespace LingosBotApp;

internal sealed class LessonRunner (
    IWebDriver driver,
    AppConfig config,
    IReadOnlyDictionary<string, IReadOnlyList<string>> vocabulary)
{
    private readonly IWebDriver _driver = driver;
    private readonly AppConfig _config = config;
    private readonly IReadOnlyDictionary<string, IReadOnlyList<string>> _vocabulary = vocabulary;
    private readonly IReadOnlyDictionary<string, IReadOnlyList<string>> _reverseVocabulary = BuildReverseVocabulary(vocabulary);
    private readonly Random _random = new();

    public void RunLesson(int lessonNumber)
    {
        Console.WriteLine($"Preparing lesson {lessonNumber}...");
        OpenMainPage();
        StartLesson();

        var answeredPrompts = 0;

        while (answeredPrompts < _config.EffectiveLessonPromptSafetyCap)
        {
            if (IsLessonFinished())
            {
                Console.WriteLine($"Lesson {lessonNumber} completed after {answeredPrompts} prompts.");
                return;
            }

            var step = WaitForStepOrLessonFinished();
            if (step.Kind == LessonStepKind.Finished)
            {
                Console.WriteLine($"Lesson {lessonNumber} completed after {answeredPrompts} prompts.");
                return;
            }

            if (step.Kind == LessonStepKind.ContinueOnly)
            {
                ContinueInformationalStep();
                continue;
            }

            if (step.Kind == LessonStepKind.Blocked)
            {
                ThrowLessonLimitReached();
            }

            answeredPrompts++;
            var promptElement = step.PromptElement!;
            var promptText = TextNormalizer.Normalize(promptElement.Text);

            if (string.IsNullOrWhiteSpace(promptText))
            {
                throw new InvalidOperationException(
                    "The lesson prompt text was empty. Verify the lesson prompt selector in Selectors.cs.");
            }

            var candidateAnswers = ResolveCandidateAnswers(promptText);
            if (candidateAnswers.Count == 0)
            {
                throw new InvalidOperationException(
                    $"No collected translation exists for lesson prompt '{promptText}'. The bot stopped safely without guessing.");
            }

            var lessonFinished = TryAnswerPromptWithCandidates(promptText, candidateAnswers, lessonNumber, answeredPrompts);
            if (lessonFinished)
            {
                Console.WriteLine($"Lesson {lessonNumber} completed.");
                return;
            }
        }

        throw new InvalidOperationException(
            $"Lesson {lessonNumber} exceeded the safety cap of {_config.EffectiveLessonPromptSafetyCap} prompts. Stopping to avoid an infinite loop.");
    }

    private void OpenMainPage()
    {
        var currentUrl = _driver.Url ?? string.Empty;
        var lessonPageVisible = currentUrl.Contains("/learning/start/", StringComparison.OrdinalIgnoreCase);

        if (!lessonPageVisible &&
            TryFindVisible(_driver, Selectors.MainLearnButton.ToBy(), out var existingLearnButton) &&
            existingLearnButton is not null &&
            existingLearnButton.Enabled)
        {
            return;
        }

        if (!lessonPageVisible &&
            TryWaitUntilClickable(Selectors.MainLearnButton, _config.LessonRestartReuseTimeout, out var delayedLearnButton) &&
            delayedLearnButton is not null)
        {
            return;
        }

        _driver.Navigate().GoToUrl(_config.StudentDashboardUrl);
        WaitForDocumentReady();
        WaitUntilClickable(Selectors.MainLearnButton);
    }

    private void StartLesson()
    {
        Console.WriteLine("Starting a lesson from the main page...");
        const int maxEntryClicks = 3;

        for (var entryClick = 1; entryClick <= maxEntryClicks; entryClick++)
        {
            var mainLearnButton = WaitUntilClickable(Selectors.MainLearnButton);
            ClickElement(mainLearnButton);

            var entryState = CreateWait(_config.ShortWaitTimeout).Until(_ =>
            {
                var state = ReadLessonEntryState();
                return state == LessonEntryState.Waiting ? null : state.ToString();
            }) ?? LessonEntryState.Waiting.ToString();

            if (string.Equals(entryState, LessonEntryState.Blocked.ToString(), StringComparison.Ordinal))
            {
                ThrowIfLessonLimitReached();
            }

            if (string.Equals(entryState, LessonEntryState.Ready.ToString(), StringComparison.Ordinal))
            {
                return;
            }

            if (string.Equals(entryState, LessonEntryState.ResumeRequired.ToString(), StringComparison.Ordinal))
            {
                Console.WriteLine("Lingos opened an intermediate 'Dokończ lekcję' page. Continuing into the actual lesson...");
                continue;
            }
        }

        throw new WebDriverTimeoutException(
            "Timed out while entering the lesson. Lingos did not show a prompt, continue step, or lesson-limit message.");
    }

    private bool TryAnswerPromptWithCandidates(
        string promptText,
        IReadOnlyList<string> candidateAnswers,
        int lessonNumber,
        int promptIndex)
    {
        if (MakeAnError())
        {
            var wrongAnswer = GenerateWrongAnswer(candidateAnswers[0]);
            Console.WriteLine($"[Lesson {lessonNumber} · word {promptIndex}] {promptText} -> {wrongAnswer} [intentional error]");

            var errorOutcome = SubmitBestEffortAnswer(wrongAnswer);
            if (errorOutcome == LessonAnswerOutcome.Finished)
            {
                return true;
            }

            if (errorOutcome != LessonAnswerOutcome.Rejected)
            {
                throw new InvalidOperationException(
                    $"The intentional wrong answer for '{promptText}' was unexpectedly accepted.");
            }

            // Lingos shows the result screen after a wrong answer. Dismiss it
            // before submitting the correct translation for the same prompt.
            ContinueAfterAnswer(promptText);
        }

        for (var candidateIndex = 0; candidateIndex < candidateAnswers.Count; candidateIndex++)
        {
            var answer = candidateAnswers[candidateIndex];

            var isLastCandidate = candidateIndex == candidateAnswers.Count - 1;

            if (candidateAnswers.Count > 1 || candidateIndex > 0)
            {
                Console.WriteLine(
                    $"[Lesson {lessonNumber} · word {promptIndex}] {promptText} -> {answer}  (option {candidateIndex + 1}/{candidateAnswers.Count})");
            }
            else
            {
                Console.WriteLine($"[Lesson {lessonNumber} · word {promptIndex}] {promptText} -> {answer}");
            }

            var outcome = SubmitBestEffortAnswer(answer);

            if (outcome == LessonAnswerOutcome.Accepted)
            {
                return ContinueAfterAnswer(promptText);
            }

            if (outcome == LessonAnswerOutcome.Finished)
            {
                return true;
            }

            if (!isLastCandidate)
            {
                Console.WriteLine(
                    $"Answer '{answer}' was rejected for '{promptText}'. Trying the next stored translation for this exact prompt only.");
                continue;
            }

            throw new InvalidOperationException(
                $"All {candidateAnswers.Count} stored answers were rejected for lesson prompt '{promptText}'.");
        }

        return false;
    }

    private LessonAnswerOutcome SubmitBestEffortAnswer(string answer)
    {
        var answerInput = WaitUntilClickable(Selectors.LessonAnswerInput);
        SubmitAnswer(answerInput, answer);
        return WaitForAnswerOutcome();
    }

    private void SubmitAnswer(IWebElement answerInput, string answer)
    {
        var populatedWithFastPath = TryPopulateAnswerInput(answerInput, answer);

        ScrollIntoView(answerInput);
        if (!populatedWithFastPath)
        {
            answerInput.SendKeys(Keys.Control + "a");
            answerInput.SendKeys(Keys.Delete);
            answerInput.SendKeys(answer);
        }

        if (TryClickAnswerActionButton())
        {
            return;
        }

        answerInput.SendKeys(Keys.Enter);
    }

    private bool TryPopulateAnswerInput(IWebElement answerInput, string answer)
    {
        try
        {
            var result = ((IJavaScriptExecutor)_driver).ExecuteScript(
                """
                const input = arguments[0];
                const value = arguments[1];

                const descriptor =
                    Object.getOwnPropertyDescriptor(Object.getPrototypeOf(input), 'value') ||
                    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value');

                if (!descriptor || typeof descriptor.set !== 'function') {
                    return false;
                }

                input.focus();
                descriptor.set.call(input, '');
                input.dispatchEvent(new Event('input', { bubbles: true }));
                descriptor.set.call(input, value);
                input.dispatchEvent(new Event('input', { bubbles: true }));
                input.dispatchEvent(new Event('change', { bubbles: true }));
                return true;
                """,
                answerInput,
                answer);

            return result is bool populated && populated;
        }
        catch (Exception)
        {
            return false;
        }
    }

    private bool TryClickAnswerActionButton()
    {
        try
        {
            if (!TryFindVisible(_driver, Selectors.LessonContinueButton.ToBy(), out var button) ||
                button is null ||
                !button.Enabled)
            {
                return false;
            }

            ClickElement(button);
            return true;
        }
        catch (Exception)
        {
            return false;
        }
    }

    private LessonAnswerOutcome WaitForAnswerOutcome()
    {
        var feedbackBy = Selectors.LessonFeedbackMarker.ToBy();
        var incorrectBy = Selectors.LessonIncorrectAnswerMarker.ToBy();
        var continueBy = Selectors.LessonContinueButton.ToBy();

        var result = CreateWait().Until(driver =>
        {
            if (IsLessonFinished())
            {
                return "finished";
            }

            if (!TryFindVisible(driver, feedbackBy, out _))
            {
                return null;
            }

            if (TryFindVisible(driver, incorrectBy, out var incorrectElement) &&
                incorrectElement is not null &&
                !string.IsNullOrWhiteSpace(incorrectElement.Text))
            {
                return "rejected";
            }

            // The new UI shows a single "Dalej" button for both outcomes;
            // the red feedback marker above is the reliable rejection signal.
            if (TryFindVisible(driver, continueBy, out _))
            {
                return "accepted";
            }

            return null;
        }) ?? throw new WebDriverTimeoutException("Timed out waiting for the lesson to accept or reject the submitted answer.");

        return result switch
        {
            "accepted" => LessonAnswerOutcome.Accepted,
            "rejected" => LessonAnswerOutcome.Rejected,
            "finished" => LessonAnswerOutcome.Finished,
            _ => throw new InvalidOperationException($"Unexpected lesson outcome '{result}'.")
        };
    }

    private bool IsLessonFinished()
    {
        return (_driver.Url ?? string.Empty).Contains("/group/finished", StringComparison.OrdinalIgnoreCase);
    }

    private bool ContinueAfterAnswer(string previousPromptText)
    {
        if (IsLessonFinished())
        {
            return true;
        }

        var continueButton = WaitUntilClickable(Selectors.LessonContinueButton);
        ClickElement(continueButton);

        CreateWait().Until(_ =>
        {
            if (IsLessonFinished())
            {
                return true;
            }

            var currentStep = ReadCurrentLessonStep();
            if (currentStep.Kind == LessonStepKind.Blocked)
            {
                ThrowLessonLimitReached();
            }

            if (currentStep.Kind == LessonStepKind.ContinueOnly)
            {
                return true;
            }

            if (currentStep.Kind != LessonStepKind.Prompt || currentStep.PromptElement is null)
            {
                return false;
            }

            if (!HasVisibleFeedback())
            {
                return true;
            }

            var currentPromptText = TextNormalizer.Normalize(currentStep.PromptElement.Text);
            return !string.Equals(currentPromptText, previousPromptText, StringComparison.OrdinalIgnoreCase);
        });

        return IsLessonFinished();
    }

    private void ContinueInformationalStep()
    {
        if (IsLessonFinished())
        {
            return;
        }

        var continueButton = WaitUntilClickable(Selectors.LessonContinueButton);
        ClickElement(continueButton);

        CreateWait().Until(_ =>
        {
            if (IsLessonFinished())
            {
                return true;
            }

            var currentStep = ReadCurrentLessonStep();
            return currentStep.Kind != LessonStepKind.ContinueOnly;
        });
    }

    private bool HasVisibleFeedback()
    {
        return TryFindVisible(_driver, Selectors.LessonFeedbackMarker.ToBy(), out _);
    }

    private LessonStepState WaitForStepOrLessonFinished()
    {
        return CreateWait().Until(_ =>
        {
            var step = ReadCurrentLessonStep();
            return step.Kind == LessonStepKind.Waiting ? null : step;
        }) ?? throw new WebDriverTimeoutException("Timed out waiting for the next lesson step.");
    }

    private LessonStepState ReadCurrentLessonStep()
    {
        if (HasLessonLimitReached())
        {
            return new LessonStepState(LessonStepKind.Blocked, null);
        }

        if (IsLessonFinished())
        {
            return new LessonStepState(LessonStepKind.Finished, null);
        }

        var promptVisible = TryFindVisible(_driver, Selectors.LessonPrompt.ToBy(), out var promptElement) && promptElement is not null;
        var inputVisible = TryFindVisible(_driver, Selectors.LessonAnswerInput.ToBy(), out _);
        var continueVisible = TryFindVisible(_driver, Selectors.LessonContinueButton.ToBy(), out _);

        if (promptVisible && inputVisible)
        {
            return new LessonStepState(LessonStepKind.Prompt, promptElement);
        }

        if (continueVisible && !inputVisible)
        {
            return new LessonStepState(LessonStepKind.ContinueOnly, null);
        }

        return new LessonStepState(LessonStepKind.Waiting, null);
    }

    private LessonEntryState ReadLessonEntryState()
    {
        if (HasLessonLimitReached())
        {
            return LessonEntryState.Blocked;
        }

        if (ReadCurrentLessonStep().Kind != LessonStepKind.Waiting)
        {
            return LessonEntryState.Ready;
        }

        var currentUrl = _driver.Url ?? string.Empty;
        if (currentUrl.Contains("/learning/start", StringComparison.OrdinalIgnoreCase) &&
            TryFindVisible(_driver, Selectors.MainLearnButton.ToBy(), out var resumeButton) &&
            resumeButton is not null &&
            resumeButton.Enabled)
        {
            return LessonEntryState.ResumeRequired;
        }

        return LessonEntryState.Waiting;
    }

    private bool HasLessonLimitReached()
    {
        return _driver.FindElements(Selectors.LessonLimitReachedMarker.ToBy()).Count > 0;
    }

    private void ThrowIfLessonLimitReached()
    {
        if (!HasLessonLimitReached())
        {
            return;
        }

        ThrowLessonLimitReached();
    }

    private static void ThrowLessonLimitReached()
    {
        throw new LessonLimitReachedException(
            "Lingos reported that today's lesson limit has been reached for this account. The bot stopped cleanly.");
    }

    private IReadOnlyList<string> ResolveCandidateAnswers(string prompt)
    {
        if (_vocabulary.TryGetValue(prompt, out var directMatches))
        {
            return directMatches;
        }

        if (_reverseVocabulary.TryGetValue(prompt, out var reverseMatches))
        {
            Console.WriteLine($"Prompt '{prompt}' matched the reverse vocabulary lookup.");
            return reverseMatches;
        }

        return [];
    }

    private static IReadOnlyDictionary<string, IReadOnlyList<string>> BuildReverseVocabulary(
        IReadOnlyDictionary<string, IReadOnlyList<string>> vocabulary)
    {
        var reverse = new Dictionary<string, List<string>>(StringComparer.OrdinalIgnoreCase);

        foreach (var pair in vocabulary)
        {
            foreach (var foreignWord in pair.Value)
            {
                var normalizedForeignWord = TextNormalizer.Normalize(foreignWord);
                if (string.IsNullOrWhiteSpace(normalizedForeignWord))
                {
                    continue;
                }

                if (!reverse.TryGetValue(normalizedForeignWord, out var candidates))
                {
                    candidates = new List<string>();
                    reverse[normalizedForeignWord] = candidates;
                }

                if (!candidates.Any(candidate => string.Equals(candidate, pair.Key, StringComparison.OrdinalIgnoreCase)))
                {
                    candidates.Add(pair.Key);
                }
            }
        }

        return reverse.ToDictionary(pair => pair.Key, pair => (IReadOnlyList<string>)pair.Value.AsReadOnly(), StringComparer.OrdinalIgnoreCase);
    }

    private bool TryFindVisible(ISearchContext context, By by, out IWebElement? element)
    {
        try
        {
            element = context.FindElement(by);
            return element.Displayed;
        }
        catch (NoSuchElementException)
        {
            element = null;
            return false;
        }
        catch (StaleElementReferenceException)
        {
            element = null;
            return false;
        }
    }

    private IWebElement WaitUntilClickable(SelectorDefinition selector)
    {
        var by = selector.ToBy();

        return CreateWait().Until(driver =>
        {
            try
            {
                var element = driver.FindElement(by);
                return element.Displayed && element.Enabled ? element : null;
            }
            catch (NoSuchElementException)
            {
                return null;
            }
            catch (StaleElementReferenceException)
            {
                return null;
            }
        }) ?? throw new WebDriverTimeoutException($"Timed out waiting for selector '{selector.Name}' to become clickable.");
    }

    private bool TryWaitUntilClickable(SelectorDefinition selector, TimeSpan timeout, out IWebElement? element)
    {
        try
        {
            element = WaitUntilClickable(selector, timeout);
            return true;
        }
        catch (WebDriverTimeoutException)
        {
            element = null;
            return false;
        }
    }

    private IWebElement WaitUntilClickable(SelectorDefinition selector, TimeSpan timeout)
    {
        var by = selector.ToBy();

        return CreateWait(timeout).Until(driver =>
        {
            try
            {
                var clickableElement = driver.FindElement(by);
                return clickableElement.Displayed && clickableElement.Enabled ? clickableElement : null;
            }
            catch (NoSuchElementException)
            {
                return null;
            }
            catch (StaleElementReferenceException)
            {
                return null;
            }
        }) ?? throw new WebDriverTimeoutException($"Timed out waiting for selector '{selector.Name}' to become clickable.");
    }

    private void WaitForDocumentReady()
    {
        CreateWait().Until(driver =>
        {
            var state = ((IJavaScriptExecutor)driver).ExecuteScript("return document.readyState");
            return string.Equals(state?.ToString(), "complete", StringComparison.OrdinalIgnoreCase);
        });
    }

    private WebDriverWait CreateWait(TimeSpan? timeout = null)
    {
        var wait = new WebDriverWait(new SystemClock(), _driver, timeout ?? _config.DefaultWaitTimeout, _config.PollingInterval);
        wait.IgnoreExceptionTypes(typeof(NoSuchElementException), typeof(StaleElementReferenceException));
        return wait;
    }

    private void ClickElement(IWebElement element)
    {
        ScrollIntoView(element);

        try
        {
            element.Click();
        }
        catch (ElementClickInterceptedException)
        {
            ((IJavaScriptExecutor)_driver).ExecuteScript("arguments[0].click();", element);
        }
    }

    private void ScrollIntoView(IWebElement element)
    {
        ((IJavaScriptExecutor)_driver).ExecuteScript(
            "arguments[0].scrollIntoView({block: 'center', inline: 'center'});",
            element);
    }

    private bool MakeAnError()
    {
        return _random.NextDouble() < _config.ErrorsPer100Words / 100.0;
    }

    private string GenerateWrongAnswer(string correctAnswer)
    {
        if (string.IsNullOrEmpty(correctAnswer))
        {
            return "wrong";
        }

        if (correctAnswer.Length == 1)
        {
            var letters = "abcdefghijklmnopqrstuvwxyz";
            var wrongCharacter = letters[_random.Next(letters.Length)];
            while (char.ToUpperInvariant(wrongCharacter) == char.ToUpperInvariant(correctAnswer[0]))
            {
                wrongCharacter = letters[_random.Next(letters.Length)];
            }

            return wrongCharacter.ToString();
        }

        var chars = correctAnswer.ToCharArray();
        var randomIndex = _random.Next(chars.Length);
        var randomChar = (char)('a' + _random.Next(26));
        while (char.ToUpperInvariant(randomChar) == char.ToUpperInvariant(chars[randomIndex]))
        {
            randomChar = (char)('a' + _random.Next(26));
        }
        chars[randomIndex] = randomChar;
        return new string(chars);
    }
}

internal enum LessonAnswerOutcome
{
    Accepted,
    Rejected,
    Finished
}

internal enum LessonStepKind
{
    Waiting,
    Prompt,
    ContinueOnly,
    Blocked,
    Finished
}

internal enum LessonEntryState
{
    Waiting,
    Ready,
    ResumeRequired,
    Blocked
}

internal sealed record LessonStepState(LessonStepKind Kind, IWebElement? PromptElement);

internal sealed class LessonLimitReachedException(string message)
    : Exception(message) { }

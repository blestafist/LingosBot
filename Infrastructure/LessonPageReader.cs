using OpenQA.Selenium;

namespace LingosBotApp;

// Read a consistent DOM snapshot in one browser command instead of querying
// each marker and its Displayed/Text properties separately. Never cache across
// commands: React can update the lesson without navigating.
internal sealed class LessonPageReader(IWebDriver driver)
{
    public LessonPageSnapshot? TryRead()
    {
        if (driver is not IJavaScriptExecutor executor)
        {
            return null;
        }

        try
        {
            var result = executor.ExecuteScript(ReadScript,
                Selectors.LessonPrompt.Value,
                Selectors.LessonAnswerInput.Value,
                Selectors.LessonContinueButton.Value,
                Selectors.LessonFeedbackMarker.Value,
                Selectors.LessonIncorrectAnswerMarker.Value,
                Selectors.LessonFinishedMarker.Value,
                Selectors.LessonLimitReachedMarker.Value) as IDictionary<string, object>;

            if (result is null)
            {
                return null;
            }

            return new LessonPageSnapshot(
                (bool)result["finished"],
                (bool)result["blocked"],
                result["prompt"] as IWebElement,
                (bool)result["input"],
                (bool)result["continueButton"],
                (bool)result["feedback"],
                (bool)result["incorrect"]);
        }
        catch (WebDriverException)
        {
            // Preserve the Selenium path for unsupported browsers or a document
            // being replaced during navigation.
            return null;
        }
    }

    internal const string ReadScript =
        """
        // checkVisibility is unavailable in some older supported browsers.
        // Return to Selenium rather than approximating its visibility semantics.
        if (typeof Element.prototype.checkVisibility !== 'function') return null;
        const visible = element => element.checkVisibility({
            checkOpacity: true, checkVisibilityCSS: true
        }) && element.getClientRects().length > 0;
        const find = selector => Array.from(document.querySelectorAll(selector)).find(visible);
        const prompt = find(arguments[0]);
        const input = find(arguments[1]);
        const continueButton = find(arguments[2]);
        const feedback = find(arguments[3]);
        const incorrect = find(arguments[4]);
        const headings = Array.from(document.querySelectorAll(arguments[5]));
        const finished = location.href.toLowerCase().includes('/group/finished') ||
            Array.from(new URL(location.href).searchParams).some(([key, value]) =>
                key.toLowerCase() === 'finished' && value === '1') ||
            headings.some(heading => visible(heading) &&
                (heading.innerText || '').replace(/\s+/g, ' ').trim()
                    .toLowerCase().includes('lekcja wykonana'));
        return {
            finished,
            blocked: !!document.querySelector(arguments[6]),
            prompt: prompt || null,
            input: !!input,
            continueButton: !!continueButton,
            feedback: !!feedback,
            incorrect: !!incorrect && !!(incorrect.innerText || '').trim()
        };
        """;
}

internal sealed record LessonPageSnapshot(
    bool Finished,
    bool Blocked,
    IWebElement? Prompt,
    bool InputVisible,
    bool ContinueVisible,
    bool FeedbackVisible,
    bool Incorrect);

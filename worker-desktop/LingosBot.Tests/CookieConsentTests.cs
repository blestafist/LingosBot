using Xunit;

namespace LingosBotApp.Tests;

public sealed class CookieConsentTests
{
    [Fact]
    public void CookieSelectorOnlyTargetsRejectionActions()
    {
        var selector = Selectors.CookieRejectButton.Value;

        Assert.Contains("Decline", selector, StringComparison.Ordinal);
        Assert.DoesNotContain("Accept", selector, StringComparison.OrdinalIgnoreCase);
        Assert.DoesNotContain("Allow", selector, StringComparison.OrdinalIgnoreCase);
    }
}

using System.Globalization;
using System.Text;

namespace DobbyVPN.Windows;

internal static class ErrorDisplayText
{
    internal const int Limit = 32768;
    internal const string TruncationNotice = "\n\n[Error details truncated. Copy for the full original.]";

    /// <summary>Cuts the display text at a whole text element before the scalar limit and appends the truncation notice.</summary>
    internal static string ForDisplay(string original)
    {
        var scalars = 0;
        var length = 0;
        var elements = StringInfo.GetTextElementEnumerator(original);
        while (elements.MoveNext())
        {
            var element = elements.GetTextElement();
            var elementScalars = element.EnumerateRunes().Count();
            if (scalars + elementScalars > Limit)
            {
                if (scalars > 0) return original[..length] + TruncationNotice;
                // One text element alone exceeds the scalar limit; a rune-boundary
                // cut of that element keeps the display bounded.
                var kept = 0;
                var cut = 0;
                foreach (var rune in element.EnumerateRunes())
                {
                    if (kept == Limit) break;
                    kept++;
                    cut += rune.Utf16SequenceLength;
                }
                return element[..cut] + TruncationNotice;
            }
            scalars += elementScalars;
            length += element.Length;
        }
        return original;
    }

    // A scalar bound still matters inside one paragraph: issue #332 retained
    // findings record ~185 s for assigning one unbounded rich-text payload
    // versus ~14 ms when native text elements stay bounded, and a long
    // single-line error reaches the 32768-scalar display cap within one logical
    // line. The bound therefore moves to Run level: runs split inside a
    // Paragraph, which wraps continuously and introduces no hard break.
    internal const int RunScalarLimit = 256;

    /// <summary>Splits text at line breaks only, dropping the break characters and keeping empty lines.</summary>
    internal static IReadOnlyList<string> Paragraphs(string text)
    {
        var result = new List<string>();
        var start = 0;
        for (var index = 0; index < text.Length; index++)
        {
            var character = text[index];
            if (character != '\r' && character != '\n') continue;
            result.Add(text[start..index]);
            if (character == '\r' && index + 1 < text.Length && text[index + 1] == '\n') index++;
            start = index + 1;
        }
        result.Add(text[start..]);
        return result;
    }

    /// <summary>Splits one paragraph into text-element-aligned runs that stay within the scalar limit.</summary>
    internal static IReadOnlyList<string> Runs(string paragraph)
    {
        var result = new List<string>();
        var current = new StringBuilder();
        var count = 0;
        var elements = StringInfo.GetTextElementEnumerator(paragraph);
        while (elements.MoveNext())
        {
            var element = elements.GetTextElement();
            var scalars = element.EnumerateRunes().Count();
            if (scalars <= RunScalarLimit)
            {
                if (count + scalars > RunScalarLimit) Flush();
                current.Append(element);
                count += scalars;
                continue;
            }
            // An unusually large grapheme still needs a bounded native block.
            for (var index = 0; index < element.Length;)
            {
                if (count == RunScalarLimit) Flush();
                var length = char.IsHighSurrogate(element[index]) && index + 1 < element.Length &&
                    char.IsLowSurrogate(element[index + 1]) ? 2 : 1;
                current.Append(element, index, length);
                index += length;
                count++;
            }
        }
        Flush();
        return result;

        void Flush()
        {
            if (current.Length == 0) return;
            result.Add(current.ToString());
            current.Clear();
            count = 0;
        }
    }

}

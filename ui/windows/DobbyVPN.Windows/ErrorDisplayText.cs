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
    internal const int ParagraphScalarLimit = 256;

    internal static IReadOnlyList<string> Paragraphs(string text)
    {
        var result = new List<string>();
        var current = new StringBuilder();
        var count = 0;
        var elements = StringInfo.GetTextElementEnumerator(text);
        while (elements.MoveNext())
        {
            var element = elements.GetTextElement();
            var scalars = element.EnumerateRunes().Count();
            if (scalars <= ParagraphScalarLimit)
            {
                if (count + scalars > ParagraphScalarLimit) Flush();
                current.Append(element);
                count += scalars;
                continue;
            }
            // An unusually large grapheme still needs a bounded native block.
            for (var index = 0; index < element.Length;)
            {
                if (count == ParagraphScalarLimit) Flush();
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

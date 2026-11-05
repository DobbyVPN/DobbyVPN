using System.Globalization;
using System.Text;

namespace DobbyVPN.Windows;

internal static class ErrorDisplayText
{
    internal const int Limit = 32768;
    internal const string TruncationNotice = "\n\n[Error details truncated. Copy for the full original.]";

    internal static string ForDisplay(string original)
    {
        var count = 0;
        var length = 0;
        foreach (var rune in original.EnumerateRunes())
        {
            if (count++ == Limit) return original[..length] + TruncationNotice;
            length += rune.Utf16SequenceLength;
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

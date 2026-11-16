# Count UTF-16 code units conservatively for App Store Connect's character limit.
module IosReleaseNotes
  FORBIDDEN = ["Android", "Windows", "Linux", "macOS", "F-Droid", "Google Play"].freeze

  def self.validate!(notes)
    raise ArgumentError, "iOS release notes must be nonblank UTF-8 text" unless notes.is_a?(String) && notes.valid_encoding? && !notes.match?(/\A[[:space:]\x1c-\x1f]*\z/)
    raise ArgumentError, "iOS release notes exceed 4000 Apple characters" if notes.encode("UTF-16LE").bytesize / 2 > 4000

    found = FORBIDDEN.select do |term|
      notes.match?(/(?<![\p{L}\p{N}_])#{Regexp.escape(term)}(?![\p{L}\p{N}_])/i)
    end
    raise ArgumentError, "iOS release notes contain prohibited terms: #{found.join(', ')}" unless found.empty?

    notes
  end
end

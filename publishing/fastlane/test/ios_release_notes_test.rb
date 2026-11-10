require "minitest/autorun"
require_relative "../ios_release_notes"

class IosReleaseNotesTest < Minitest::Test
  def test_preserves_text_and_boundaries
    notes = "  Improved androidness on iOS.\nLine two.\n"
    assert_same notes, IosReleaseNotes.validate!(notes)
    assert_equal "x" * 4000, IosReleaseNotes.validate!("x" * 4000)
    assert_equal "😀" * 2000, IosReleaseNotes.validate!("😀" * 2000)
  end

  def test_rejects_blank_and_overlong_notes
    ["", " \n\t", "x" * 4001, "😀" * 2001, "\u001c"].each do |notes|
      assert_raises(ArgumentError) { IosReleaseNotes.validate!(notes) }
    end
  end

  def test_reports_all_standalone_terms_case_insensitively
    error = assert_raises(ArgumentError) do
      IosReleaseNotes.validate!("ANDROID, windows; Linux / MACOS / f-droid / google play")
    end
    assert_equal "iOS release notes contain prohibited terms: Android, Windows, Linux, macOS, F-Droid, Google Play", error.message
    assert_equal "androidness Google Playful ²Android", IosReleaseNotes.validate!("androidness Google Playful ²Android")
  end
end

require "minitest/autorun"
require "open3"
require "tmpdir"
require "yaml"

ROOT = File.expand_path("../../..", __dir__)
WORKFLOW = YAML.load_file(File.join(ROOT, ".github/workflows/release.yml"))

# Load the actual Fastfile with a minimal Fastlane DSL. Any connection attempt
# fails so the tests can distinguish validation from publication.
module UI
  def self.user_error!(message)
    raise ArgumentError, message
  end
end
$delivery_lanes = {}
def platform(_name, &block) = block.call
def desc(_description); end
def lane(name, &block) = $delivery_lanes[name] = block
def app_store_connect_api_key(**_args) = raise("Apple connection reached")
load File.join(ROOT, "publishing/fastlane/Fastfile")

class DeliveryIntegrationTest < Minitest::Test
  def test_workflow_preserves_input_bytes_through_notes_file
    notes = "  Améliorations 😀\nDeuxième ligne  \n\n"
    Dir.mktmpdir("delivery-test-") do |dir|
      output = File.join(dir, "output")
      env_file = File.join(dir, "env")
      File.write(output, "")
      File.write(env_file, "")
      jobs = WORKFLOW.fetch("jobs")
      resolve = jobs.fetch("resolve_publish")
      assert_equal "${{ inputs.ios_release_notes }}", resolve.fetch("steps").find { |s| s["id"] == "ios_notes" }.fetch("env").fetch("IOS_RELEASE_NOTES")
      validate = resolve.fetch("steps").find { |s| s["id"] == "ios_notes" }.fetch("run")
      run_step(validate, "IOS_RELEASE_NOTES" => notes, "GITHUB_OUTPUT" => output)
      b64 = File.read(output).match(/\Aios_release_notes_b64=(.*)\n\z/)[1]
      assert_equal "${{ steps.ios_notes.outputs.ios_release_notes_b64 }}", resolve.fetch("outputs").fetch("ios_release_notes_b64")
      metadata = jobs.fetch("app_store").fetch("steps").find { |s| s["name"] == "Set selected release metadata and exact notes" }
      assert_equal "${{ needs.resolve_publish.outputs.ios_release_notes_b64 }}", metadata.fetch("env").fetch("RELEASE_NOTES_B64")
      # Replace only the unrelated GitHub expression; the shell step itself is real.
      script = metadata.fetch("run").sub("${{ needs.resolve_publish.outputs.android_version_code }}", "1000000")
      run_step(script, "VERSION_NAME" => "1.0.0", "APPLE_BUILD_NUMBER" => "1000000", "SOURCE_SHA" => "a" * 40,
               "RELEASE_NOTES_B64" => b64, "RUNNER_TEMP" => dir, "GITHUB_ENV" => env_file)
      notes_path = File.read(env_file).match(/^APP_STORE_RELEASE_NOTES_FILE=(.*)$/)[1]
      assert_equal notes.b, File.binread(notes_path)
      assert_equal "resolve_publish", jobs.fetch("app_store").fetch("needs")
      assert_equal "resolve_publish", jobs.fetch("fdroid").fetch("needs")
    end
  end

  def test_workflow_rejects_invalid_notes_before_downstream_jobs
    step = WORKFLOW.fetch("jobs").fetch("resolve_publish").fetch("steps").find { |s| s["id"] == "ios_notes" }
    Dir.mktmpdir("delivery-test-") do |dir|
      output = File.join(dir, "output")
      File.write(output, "")
      _stdout, stderr, status = Open3.capture3(
        { "IOS_RELEASE_NOTES" => "Android and Google Play", "GITHUB_OUTPUT" => output },
        "bash", "-e", "-c", step.fetch("run"), chdir: ROOT
      )
      refute status.success?
      assert_match(/Android, Google Play/, stderr)
      assert_empty File.read(output)
    end
  end

  def test_fastlane_rejects_invalid_notes_before_apple_connection
    with_lane_env("APP_STORE_RELEASE_NOTES" => "Includes Android and WINDOWS") do
      error = assert_raises(ArgumentError) { $delivery_lanes.fetch(:submit_app_store_review).call }
      assert_match(/Android, Windows/, error.message)
    end
    with_lane_env("APP_STORE_RELEASE_NOTES" => " ") do
      error = assert_raises(ArgumentError) { $delivery_lanes.fetch(:submit_app_store_review).call }
      assert_match(/nonblank/, error.message)
    end
    with_lane_env("APP_STORE_RELEASE_NOTES" => nil) do
      assert_raises(ArgumentError) { $delivery_lanes.fetch(:submit_app_store_review).call }
    end
    Dir.mktmpdir("delivery-test-") do |dir|
      path = File.join(dir, "notes")
      File.write(path, "Linux")
      with_lane_env("APP_STORE_RELEASE_NOTES" => "valid fallback", "APP_STORE_RELEASE_NOTES_FILE" => path) do
        error = assert_raises(ArgumentError) { $delivery_lanes.fetch(:submit_app_store_review).call }
        assert_match(/Linux/, error.message)
      end
    end
    with_lane_env("APP_STORE_RELEASE_NOTES" => "iOS improvements") do
      assert_equal "Apple connection reached", assert_raises(RuntimeError) { $delivery_lanes.fetch(:submit_app_store_review).call }.message
    end
  end

  private

  def run_step(script, env)
    stdout, stderr, status = Open3.capture3(env, "bash", "-e", "-c", script, chdir: ROOT)
    assert status.success?, "Workflow step failed: #{stdout}#{stderr}"
  end

  def with_lane_env(notes)
    values = { "APP_STORE_VERSION" => "1.0.0", "APP_STORE_BUILD_NUMBER" => "1000000",
               "APP_STORE_KEY_ID" => "key", "APP_STORE_ISSUER_ID" => "issuer", "APP_STORE_API_KEY" => "key",
               "APP_STORE_RELEASE_NOTES_FILE" => nil }.merge(notes)
    previous = values.to_h { |key, _| [key, ENV[key]] }
    values.each { |key, value| ENV[key] = value }
    yield
  ensure
    previous.each { |key, value| ENV[key] = value }
  end
end

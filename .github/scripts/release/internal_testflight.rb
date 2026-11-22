# Complete internal distribution only after the selected upload has processed.
require "json"
require "net/http"
require "uri"
require "digest"
require_relative "../../../publishing/fastlane/ios_release_notes"

class InternalTestflight
  def initialize(token, transport: nil, clock: nil, pause: nil)
    @token = token
    @clock = clock || -> { Process.clock_gettime(Process::CLOCK_MONOTONIC) }
    @pause = pause || ->(seconds) { sleep(seconds) }
    @transport = transport || method(:http_request)
  end

  def distribute(app_id:, build_id:, version:, number:, notes:, deadline:)
    @deadline = deadline
    IosReleaseNotes.validate!(notes)
    build = request("GET", "/v1/builds/#{build_id}").fetch("data")
    attributes = build.fetch("attributes")
    raise "Selected build is not valid, unexpired, and exact" unless attributes.fetch("version") == number && attributes.fetch("processingState") == "VALID" && attributes.fetch("expired") == false
    release = request("GET", "/v1/builds/#{build_id}/preReleaseVersion").fetch("data").fetch("attributes")
    raise "Selected build has wrong version or platform" unless release.fetch("version") == version && release.fetch("platform") == "IOS"
    owner = request("GET", "/v1/builds/#{build_id}/app").fetch("data")
    raise "Selected build belongs to another app" unless owner.fetch("id") == app_id
    groups = all("/v1/apps/#{app_id}/betaGroups").select { |g| g.fetch("attributes").fetch("name") == "Internal Testers" && g.fetch("attributes").fetch("isInternalGroup") }
    raise "Expected exactly one Internal Testers group" unless groups.length == 1
    group_id = groups.first.fetch("id")
    localizations_path = "/v1/builds/#{build_id}/betaBuildLocalizations"
    english = all(localizations_path).select { |l| l.fetch("attributes").fetch("locale") == "en-US" }
    raise "Multiple English beta localizations" if english.length > 1
    if english.empty?
      request("POST", "/v1/betaBuildLocalizations", {data: {type: "betaBuildLocalizations", attributes: {locale: "en-US", whatsNew: notes}, relationships: {build: {data: {type: "builds", id: build_id}}}}})
    elsif english.first.fetch("attributes")["whatsNew"] != notes
      id = english.first.fetch("id")
      request("PATCH", "/v1/betaBuildLocalizations/#{id}", {data: {type: "betaBuildLocalizations", id: id, attributes: {whatsNew: notes}}})
    end
    wait_until("TestFlight notes readback differs", deadline) do
      verified = all(localizations_path).select { |l| l.fetch("attributes").fetch("locale") == "en-US" }
      verified.length == 1 && verified.first.fetch("attributes")["whatsNew"] == notes
    end
    builds_path = "/v1/betaGroups/#{group_id}/builds"
    unless all(builds_path).any? { |b| b.fetch("id") == build_id }
      request("POST", "/v1/betaGroups/#{group_id}/relationships/builds", {data: [{type: "builds", id: build_id}]})
    end
    wait_until("Selected build missing from Internal Testers", deadline) do
      all(builds_path).any? { |b| b.fetch("id") == build_id }
    end
    state = nil
    wait_until(-> { "Internal testing is not active: #{state}" }, deadline) do
      state = request("GET", "/v1/builds/#{build_id}/buildBetaDetail").fetch("data").fetch("attributes").fetch("internalBuildState")
      state == "IN_BETA_TESTING"
    end
    puts "Verified #{version} (#{number}) assigned to Internal Testers; state=#{state}; iOS notes SHA256=#{Digest::SHA256.hexdigest(notes)}"
  end

  private

  def wait_until(message, deadline)
    loop do
      raise(message.respond_to?(:call) ? message.call : message) if @clock.call >= deadline
      return if yield
      remaining = deadline - @clock.call
      raise(message.respond_to?(:call) ? message.call : message) if remaining <= 0
      @pause.call([5, remaining].min)
    end
  end

  def request(method, path, body = nil)
    raise "Internal distribution deadline expired before #{method} #{path}" if @clock.call >= @deadline
    result = @transport.call(method, path, body)
    raise "Internal distribution deadline expired after #{method} #{path}" if @clock.call >= @deadline
    result
  end

  def all(path)
    records = []
    visited = []
    while path
      raise "Repeated Apple pagination URL" if visited.include?(path)
      visited << path
      page = request("GET", path)
      records.concat(page.fetch("data"))
      path = page.fetch("links", {})["next"]
    end
    records
  end

  def http_request(method, path, body)
    uri = URI.join("https://api.appstoreconnect.apple.com", path)
    raise "Unexpected Apple API URL" unless uri.scheme == "https" && uri.host == "api.appstoreconnect.apple.com" && uri.port == 443
    request = Net::HTTP.const_get(method.capitalize).new(uri)
    request["Authorization"] = "Bearer #{@token}"
    request["Content-Type"] = "application/json"
    request.body = JSON.generate(body) if body
    timeout = [30, @deadline - @clock.call].min
    raise "Internal distribution deadline expired" if timeout <= 0
    response = Net::HTTP.start(uri.host, uri.port, use_ssl: true, open_timeout: timeout, read_timeout: timeout) do |http|
      http.max_retries = 0
      http.request(request)
    end
    raise "Apple #{method} #{path}: HTTP #{response.code}\n#{response.body}" unless response.is_a?(Net::HTTPSuccess)
    response.body.to_s.empty? ? {} : JSON.parse(response.body)
  end
end

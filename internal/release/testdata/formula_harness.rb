# A minimal, isolated Homebrew DSL harness. This runs the real Formula's
# install and test blocks, not an emulation of Homebrew itself.
require "pathname"
require "fileutils"
require "open3"
require "json"

class Formula
  class << self
    def desc(*); end
    def homepage(*); end
    def url(*); end
    def version(value = nil); @version = value unless value.nil?; @version; end
    def sha256(*); end
    def license(*); end
    def depends_on(*); end
    def deny_network_access!; end
    def test(&block); @test_block = block; end
    attr_reader :test_block
  end

  def initialize(root)
    @root = Pathname(root)
    bin.mkpath
    testpath.mkpath
  end

  def bin; @root/"bin"; end
  def testpath; @root/"test"; end
  def buildpath; Pathname.pwd; end
  def version; self.class.version; end
  def odie(message); raise message; end
  def std_go_args; ["-trimpath", "-ldflags=-s -w", "-o", bin/"ianchor"]; end

  def system(*args)
    if args == ["go", "mod", "download"]
      raise "fetch must use the local Go toolchain" unless ENV["GOTOOLCHAIN"] == "local"
      @fetched = true # Simulate the fetch phase without network access.
      return
    end
    raise "fetch phase was not invoked" unless @fetched
    raise "build must be offline" unless ENV["GOPROXY"] == "off" && ENV["GONOPROXY"] == "none" && ENV["GOSUMDB"] == "off"
    raise "command failed: #{args.inspect}" unless Kernel.system(*args.map(&:to_s))
  end

  def shell_output(command)
    out, status = Open3.capture2e(command)
    raise "command failed: #{command}: #{out}" unless status.success?
    out
  end

  def generate_completions_from_executable(executable, command, shells:)
    shells.each do |shell|
      out, status = Open3.capture2e(executable.to_s, command, shell.to_s)
      raise "completion #{shell} failed: #{out}" unless status.success? && !out.empty?
      dir = @root/"completions"
      dir.mkpath
      (dir/shell.to_s).write(out)
    end
  end

  def assert_match(expected, actual)
    raise "#{actual.inspect} does not contain #{expected.inspect}" unless actual.include?(expected.to_s)
  end

  def assert_equal(expected, actual)
    raise "#{actual.inspect} != #{expected.inspect}" unless expected == actual
  end

  def assert_predicate(object, method)
    raise "#{object} does not satisfy #{method}" unless object.public_send(method)
  end

  def refute_predicate(object, method)
    raise "#{object} unexpectedly satisfies #{method}" if object.public_send(method)
  end
end

load ARGV.fetch(0)
formula = Ianchor.new(ARGV.fetch(1))
FileUtils.rm_f(formula.buildpath/".ianchor-modules-fetched")
formula.fetch unless ARGV[2] == "skip-fetch"
formula.install
# The installed product must work without Node, npm or the Go toolchain in
# PATH. This isolates runtime verification from the build environment.
old_path = ENV.fetch("PATH", "")
begin
  ENV["PATH"] = File.join(ARGV.fetch(1), "empty-runtime-path")
  if ARGV[2] == "install-only"
    version, status = Open3.capture2e((formula.bin/"ianchor").to_s, "--version")
    raise "installed version mismatch: #{version}" unless status.success? && version.include?(formula.version.to_s)
  else
    formula.instance_exec(&Ianchor.test_block)
    # Exercise stateful operations only against the test-rewritten coordinator.
    home = formula.testpath/"home"
    (home/"Library/Mobile Documents/com~apple~CloudDocs").mkpath
    config = home/".zshrc"
    config.write("export IANCHOR_TEST=1\n")
    ENV["HOME"] = home.to_s
    executable = (formula.bin/"ianchor").to_s
    run = lambda do |*args|
      output, status = Open3.capture2e(executable, *args.map(&:to_s))
      raise "command failed: #{args.inspect}: #{output}" unless status.success?
      output
    end
    run.call("init", "--dir", "ianchor")
    run.call("add", config)
    raise "config is not a symlink" unless config.symlink?
    entries = JSON.parse(run.call("list", "--json"))
    expected = { "name" => "zshrc", "original_path" => config.to_s, "type" => "file", "status" => "linked" }
    raise "unexpected entries: #{entries.inspect}" unless entries.size == 1 && expected.all? { |key, value| entries[0][key] == value }
    run.call("recover", "zshrc")
    raise "config was not recovered" unless !config.symlink? && config.read == "export IANCHOR_TEST=1\n"
  end
ensure
  ENV["PATH"] = old_path
end
raise "bash completion missing" unless (formula.bin.parent/"completions/bash").file?
raise "zsh completion missing" unless (formula.bin.parent/"completions/zsh").file?

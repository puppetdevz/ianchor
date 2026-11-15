# A minimal, isolated Homebrew DSL harness. This runs the real Formula's
# install and test blocks, not an emulation of Homebrew itself.
require "pathname"
require "fileutils"
require "open3"

class Formula
  class << self
    def desc(*); end
    def homepage(*); end
    def url(*); end
    def version(value = nil); @version = value unless value.nil?; @version; end
    def sha256(*); end
    def license(*); end
    def depends_on(*); end
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
  def version; self.class.version; end

  def system(*args)
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
formula.install
# The installed product must work without Node, npm or the Go toolchain in
# PATH. This isolates runtime verification from the build environment.
old_path = ENV.fetch("PATH", "")
begin
  ENV["PATH"] = File.join(ARGV.fetch(1), "empty-runtime-path")
  formula.instance_exec(&Ianchor.test_block)
ensure
  ENV["PATH"] = old_path
end
raise "bash completion missing" unless (formula.bin.parent/"completions/bash").file?
raise "zsh completion missing" unless (formula.bin.parent/"completions/zsh").file?

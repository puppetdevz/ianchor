# Exercise the rendered Formula DSL and install/test blocks with a local fixture;
# no Homebrew, network access, or native release binary is required.
require "pathname"
require "fileutils"
require "open3"

class InstallPath < Pathname
  def install(source)
    mkpath
    FileUtils.cp(source, self/source)
  end
end

class Formula
  class << self
    attr_reader :selected_url, :selected_sha, :test_block
    def desc(*); end
    def homepage(*); end
    def license(*); end
    def version(value = nil); @version = value unless value.nil?; @version; end
    def on_arm(&block); class_eval(&block) if ENV.fetch("ARCH") == "arm64"; end
    def on_intel(&block); class_eval(&block) if ENV.fetch("ARCH") == "amd64"; end
    def url(value); @selected_url = value; end
    def sha256(value); @selected_sha = value; end
    def test(&block); @test_block = block; end
  end
  def initialize(root); @root = Pathname(root); end
  def bin; InstallPath.new(@root/"bin"); end
  def version; self.class.version; end
  def generate_completions_from_executable(executable, command, shells:)
    shells.each do |shell|
      out, status = Open3.capture2e(executable.to_s, command, shell.to_s)
      raise "completion failed" unless status.success? && !out.empty?
      (@root/"completions").mkpath
      (@root/"completions"/shell.to_s).write(out)
    end
  end
  def shell_output(command)
    out, status = Open3.capture2e(command)
    raise "command failed: #{command}" unless status.success?
    out
  end
  def assert_match(expected, actual)
    raise "#{actual.inspect} does not contain #{expected.inspect}" unless actual.include?(expected.to_s)
  end
end

load ARGV.fetch(0)
raise "wrong URL" unless Ianchor.selected_url == ARGV.fetch(2)
raise "wrong SHA" unless Ianchor.selected_sha == ARGV.fetch(3)
formula = Ianchor.new(ARGV.fetch(1))
formula.install
formula.instance_exec(&Ianchor.test_block)
%w[bash zsh].each { |shell| raise "completion missing" unless (Pathname(ARGV.fetch(1))/"completions"/shell).file? }

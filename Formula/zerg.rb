# The Homebrew formula, and it BUILDS FROM SOURCE on purpose.
#
# A bottle would need one sha256 per platform kept in step with every release, and this needs
# none: `make install` produces the same toolchain on any machine that has Go and a C compiler,
# which is what building Zerg needs anyway. It is also how an Intel Mac gets a toolchain at all
# — the release publishes three native tarballs and none of them is darwin/x86_64 — so the
# formula is not a convenience beside the tarballs, it is the platform they do not reach.
#
# IT NAMES THE LAST RELEASED VERSION, never the working tree: a source tarball has no sha256
# until its tag exists. Updating the two lines below is part of cutting a release, and a formula
# that pointed at an unreleased version would install something nobody can download.
#
# WHICH IS AN INSTRUCTION, and it was followed once. 0.2.0, 0.3.0 and 0.4.0 all shipped with
# these two lines still naming v0.1.0, so `brew install` built a compiler from before generics
# or specs existed — for three releases, with nothing in the tree able to say so. `make formula`
# is the gate that can: it holds the version below to one of the two newest the changelog has a
# section for, which is the most that can be asserted while the sha has to wait for the tag.
class Zerg < Formula
  desc "Compiled language that translates to C, self-hosted and dependency-free"
  homepage "https://github.com/cmj0121/zerg"
  url "https://github.com/cmj0121/zerg/archive/refs/tags/v0.6.0.tar.gz"
  sha256 "d2fb88a51aa0aaac9cbaed27137df37da3b9795ad590645980171c48406e9c14"

  # The layers are the repository's, and LICENSE says why they differ: what ends up in YOUR
  # binary is permissive, and what produced it is not.
  license all_of: ["GPL-3.0-or-later", "MIT", "CC-BY-SA-4.0"]

  # The Go seed builds the compiler that then builds itself. Nothing is needed at run time
  # except a C compiler, which every platform Homebrew supports already has.
  depends_on "go" => :build

  def install
    # `make install` and not a copy of what it does: the repository's own rule is that this
    # target decides what an installed toolchain IS, so a formula that staged files itself
    # would be a second list to keep in step. It writes under PREFIX and nowhere else — the
    # editor wiring is `make install-editors`, which is a user's action rather than an
    # install's, and `install-check` holds both halves to that.
    system "make", "install", "PREFIX=#{prefix}"
  end

  test do
    (testpath/"hello.zg").write <<~ZERG
      fn main() {
      	print "hello, world"
      }
    ZERG

    system bin/"zerg", "build", "hello.zg"
    assert_equal "hello, world\n", shell_output(testpath/"hello")
  end
end

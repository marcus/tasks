# frozen_string_literal: true

module Tasks
  # Durable, all-or-nothing file replacement. A plain File.write can be seen
  # half-written by a concurrent reader (the live TUI, another CLI) and leaves a
  # truncated tasks.jsonl behind if the process dies mid-write. Instead write the
  # full contents to a sibling temp file, flush it to disk, then rename it over
  # the target: rename is atomic on a POSIX filesystem, so a reader — or a crash
  # — only ever sees the whole old file or the whole new one, never a torn mix.
  #
  # Because rename installs a new inode, care is taken to keep the swap
  # transparent: the target's symlink is followed (a rename over a symlink would
  # replace the link itself, orphaning a Dropbox/dotfiles setup), its permission
  # bits are carried onto the replacement (a fresh temp is born at the umask, so
  # a chmod-600 tasks.jsonl would otherwise silently widen to 644), and the parent
  # directory is fsynced after the rename so the swap is durable across a crash,
  # not merely atomic. Hardlinks are not preserved — atomic-rename replacement is
  # fundamentally incompatible with keeping a second hardlink name in sync.
  module Atomic
    module_function

    def write(path, content)
      target = resolve(path)
      dir = File.dirname(target)
      # Same directory as the target so the rename stays on one filesystem (a
      # cross-device rename is a copy, and not atomic). PID + thread id keep
      # concurrent writers to *different* files from colliding on the temp name;
      # writers to the same file are already serialized by Store's lock.
      tmp = File.join(dir, ".#{File.basename(target)}.#{Process.pid}.#{Thread.current.object_id}.tmp")
      begin
        File.open(tmp, "w", encoding: "UTF-8") do |f|
          f.write(content)
          f.flush
          f.fsync
        end
        copy_mode(target, tmp)
        File.rename(tmp, target)
        fsync_dir(dir)
      rescue StandardError
        File.delete(tmp) if File.exist?(tmp)
        raise
      end
    end

    # The concrete file the write should land on. Follow a symlink to the real
    # file so we replace *it*, not the link — even a dangling link (target on a
    # briefly-unmounted volume) is followed to its intended path rather than
    # overwritten into a plain file. A path with no link and no file yet (e.g.
    # archive.jsonl before the first sweep) is used as given.
    def resolve(path)
      if File.symlink?(path)
        File.exist?(path) ? File.realpath(path) : File.expand_path(File.readlink(path), File.dirname(path))
      elsif File.exist?(path)
        File.realpath(path) # resolves symlinked parent dirs, if any
      else
        path
      end
    end

    # Carry the existing file's permission bits onto the replacement, since a
    # fresh temp is born at the umask (a chmod-600 tasks.jsonl would otherwise widen
    # to 644). Best-effort, like fsync_dir: a filesystem that rejects chmod
    # (some CIFS/exFAT/FUSE mounts), or a target deleted out-of-band mid-write,
    # must not turn a working write into a hard failure — the write still lands,
    # just without carrying perms that the filesystem wasn't honoring anyway.
    def copy_mode(target, tmp)
      File.chmod(File.stat(target).mode, tmp)
    rescue SystemCallError
      nil
    end

    # Flush the directory entry created by the rename so the replacement
    # survives a crash. Best-effort: some platforms/filesystems refuse an fsync
    # on a directory — the rename's atomicity still holds regardless.
    def fsync_dir(dir)
      File.open(dir, &:fsync)
    rescue SystemCallError
      nil
    end
  end
end

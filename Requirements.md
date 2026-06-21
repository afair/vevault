# VeVault - Personal File Vault, Encryption, Synchronization 

Vevault manages a share of vaults stored in the User's `Vaults` directory. Each directory in the vaults is a single `vault` which can be individually managed and distributed. Vaults can be anything, such as:

- Personal (named with User name)
- Work (named with Business name)
- Home directory
- Media directories (Books, Music, Video, Pictures)
- Hosts (workstation, laptop, server)
- Applications
- Shared configurations (dot-files/stow, Keys)
- Backups (restic repos, tar files, etc)

Each vault can be subscribed to from any server with the correct ssh key. This secures different hosts accessing the same user's Vaults. But it may not handle everything. so, some api key system could be coupled with 

## Synchronization

The Distribution work can be handled by

- rsync
- rclone
- restic
- scp/sftp? 

A `central` node is selected as it holds everything and is always connected to the network to listen for updates. It performs the 2-way sync and responds to commands saying "I have updates to get"

## Encryption

Encryption is optional and needs more definition depending on possibilities.

- Could run as FUSE to serve an unencrypted shadow of an encrypted directory, platform dependent. (gocryptfs or other?)
- Copy in/out of encrypted dir will auto encrypt/decrypt. Also, list subdirs/files (names should be encrypted too)
- Compression can be enabled as well

## Backups

Backups of the Vaults should be done automatically with separate backup restic repo (or similar) for each vault.

Support 1 or more backup destinations (disks, hosts, etc) for a 3-2-1 backup strategy

Versioning of files may be too much. But maybe we can handle "move old/deleted files" to a versioned archive?

## Platforms

- MacOS
- Linux/FreeBSD Shells (all major)
- Windows (WSL? or native?)

The `vv` command (and/or the longer vevault command) is a

## Application `vv`

Should `vv` be a shell script, or written in a common install language? (perl, python, lua, node). It should be lightweight.

Subcommands: (These are initial thoughts, not final structures)

- vault
  - create name --encrypted="configuration" --backup="configuration" --dir=symlink
  - delete --yes-im-sure --delete-backups-now
  - modify
- backup
  - all (backsup all vaults to their restic repo, and vevault's config data as well)
  - vault name ... (backsup vault to its backup repo )
  - restore name location
- subscribe (a new host can subscribe to vaults)
  - vault name
  - unsub name
  - add name (adds a new untracked local vault to the central vaults)
- sync
  - all (from crontab/other, 2-way sync of data to all subscribed nodes)
  - from host/ip [name]
  - to host/ip [name]
  - vault name [host/ip]
- copy
  - clone name[/dir] directory (copies to dir, especially encrypted)
  - import name[/dir] directory (copies from dir, especially encrypted)
  - from name filelist ... directory
  - to name directory filelist ...
- mount/umount name directory -- mounts unencrypted vault as FUSE at directory
- ln name[/dir] directory -- symlinks a vault elsewhere
- other?

## Security

 Concerns:

-  same user on multiple hosts
- different user on same or different hosts
- Tool visibility. (we can have ~/.Vaults hidden with symlinks)
- user@host runs vv commands on central over ssh. Only also manage its vaults/subscriptions and trigger "i have updates." Do We need auth tokens for these commands as well?
- Manage encryption keys securely (but smoothly for usage)
- Each vault may need a secret password to prevent unauth subscriptions? Or is being "personal" enough?

We need this to be useful even if not perfect; not enterprise-level security shenanigans.


# Vevault Subscription and Sync details

This document is for ideas, final design, names, syntax can be changed to fit within the system.

## Multiple upstream Vevaults?

What would being able to subscribe to different centrals look like? 
Would we need a common option to connect like "git origin"?
This is for future if we need, but could be useful to build in now if easy.

By default, the vevault name is "vevault." We could have

    ~/.local/share/vevault   # Default
    ~/.local/share/work-vaults
    ~/.local/share/friends-vaults
    ~/.local/share/media-vaults

To specify the vault server, we can use an option or environment variable

    --vevault vevault_name
    export VEVAULT=vevault_name

## Subscriptions

### From central server:

    vv subscribe vaultname --host hostname --host ...

### From Remote:

    vv subscribe vaultname --path directory --symlink directory

How do these commands differ? It can distinguish using the host option. enough?

## Sync 

Full sync. If central (central_hostname == my_hostname?) sync all vaults to subscribers.
Otherwise (remote) ask central to sync your subscribed vaults to you.

After a full sync, changes will by on central server but not propogated out to other hosts.
A second pass is required to send changed vaults from initial sync out to other hosts.

    vv sync

### From central server:

    vv sync vaultname ... # Selected vaults to all their subscribers
    vv sync vaultname ... --pull hostname # Pull updates from here, sync everywhere
    vv sync vaultname ... --push hostname ... # Push updates to these servers

Alternate subcommands to push (tell central to pull) your changes? Clearer?

    vv publish vaultnames...
    vv push vaultname...

For pull updates, we should

- bisync vaults from remote host (pull)
- bysync vaults to all other subscribers (publish)

### From Remote:

    vv sync remote [vaultnames ...] # I have updates here
    vv sync vaultname ... --pull hostname # Pull updates from here, sync everywhere

This runs a command on the central host to sync your vaults from there.

Useful for laptops before disconnecting.

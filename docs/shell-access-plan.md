# Shell Access for Lightsail Instances

## Summary

Add an `x` key binding to open an interactive SSH shell into the selected Lightsail instance directly from the TUI.

## Approach: Lightsail Temporary SSH Keys

Lightsail has a first-party API — `GetInstanceAccessDetails` — that returns temporary SSH credentials (private key, cert key, username, IP address). This is the same mechanism the Lightsail browser-based SSH console uses. No SSM agent or EC2 Session Manager required.

### How it works

1. Call `GetInstanceAccessDetails` with the instance name and `protocol: ssh`
2. API returns:
   - `privateKey` — temporary RSA private key
   - `certKey` — signed certificate for the key
   - `username` — the default SSH user (e.g. `ubuntu`, `ec2-user`, `bitnami`)
   - `ipAddress` — public IP of the instance
   - `expiresAt` — expiry timestamp (typically 60 minutes)
3. Write the private key and cert to temp files
4. Exec `ssh` with those credentials, handing control of the terminal to the SSH process
5. Clean up temp files on return

### Implementation Plan

#### 1. Key binding (`x` on resource list)

- Only active when viewing `lightsail/instances` and instance is `running`
- Selected instance name and region are passed to the shell handler

#### 2. Fetch credentials

- Call `lightsail.GetInstanceAccessDetails` with `InstanceName` and `Protocol: ssh`
- Extract `PrivateKey`, `CertKey`, `Username`, `IpAddress` from response

#### 3. Write temp key files

- Write `privateKey` to a temp file with `0600` permissions
- Write `certKey` to `<tempfile>-cert.pub` (OpenSSH convention — SSH client auto-discovers the cert file when named this way)

#### 4. Suspend TUI and exec SSH

- Use bubbletea's `tea.ExecProcess` to hand terminal control to the SSH subprocess
- Command: `ssh -i <tempkey> -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null <username>@<ip>`
- `-o StrictHostKeyChecking=no` because the host key changes on instance recreation
- `-o UserKnownHostsFile=/dev/null` to avoid polluting the user's known_hosts
- TUI suspends while SSH is active, resumes when the user exits the shell

#### 5. Cleanup

- Delete temp key files after SSH process exits
- Refresh instance list on return (status may have changed)

### Prerequisites

- `ssh` must be available on the user's PATH (standard on macOS/Linux)
- Instance must be in `running` state with port 22 open (default for Lightsail)
- User's AWS credentials must have `lightsail:GetInstanceAccessDetails` permission

### Limitations

- Windows instances use RDP, not SSH — this feature would be Linux/Unix only
- Temporary keys expire after ~60 minutes; long sessions may disconnect
- Requires the `ssh` binary — no pure-Go SSH client (keeps the implementation simple and leverages the user's SSH config/agent)

### Alternative Considered: Pure Go SSH (x/crypto/ssh)

Using `golang.org/x/crypto/ssh` to build an in-TUI terminal emulator was considered but rejected because:
- Requires a full terminal emulator (input handling, ANSI parsing, resize events)
- Significantly more code and complexity
- Loses features users expect from their native SSH client (agent forwarding, config, multiplexing)
- `tea.ExecProcess` with native `ssh` is ~30 lines of code and gives a better UX

### UX Flow

```
Resource list → press 'x' → "Connecting to <name>..." toast
  → TUI suspends → SSH session active → user types 'exit'
  → TUI resumes → instance list refreshes
```

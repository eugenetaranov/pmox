// Package pvessh opens an SSH/SFTP session to a Proxmox VE node for the
// single purpose of writing cloud-init snippet files into a storage
// pool's on-disk snippets/ directory.
//
// It is intentionally narrow: the only filesystem operation exposed is
// UploadSnippet, which writes a single file under <storagePath>/snippets/
// via SFTP MkdirAll + atomic temp-and-rename. No arbitrary command
// execution, no reads outside the snippet path, no shell fallback.
//
// See openspec/specs/pve-node-ssh/ for the authoritative contract.
package pvessh

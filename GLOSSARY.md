# KompoSyn

Shared domain language for KompoSyn. The terms below describe file access in
the experimental temporary chat.

## Experimental chat file access

**Working directory**:
The selected directory used to resolve relative file paths. Reading a file
elsewhere does not change it or grant access to other files.
_Avoid_: Filesystem root, trusted directory.

**Read target**:
The exact file identified by a read request and presented for approval.
_Avoid_: Allowed directory, selected folder.

**Read approval**:
The user's permission for one read of an identified file for the displayed
provider destination. It does not authorize another file or a second read.
_Avoid_: Folder access, blanket read permission.

**Provider destination**:
The provider, model and authentication profile to which an approved read result
may be disclosed.
_Avoid_: Provider alone, model alone.

**File-derived context**:
Conversation material originating from a file read, including its contents and
derived responses. It remains subject to disclosure permissions and invalidation.
_Avoid_: A new read, ordinary directory selection.

**Lookup root**:
A directory bounding eligible lookups under a directory-scoped read policy.
Selecting it does not grant permission to read files.
_Avoid_: Working directory, blanket read permission.

## Proposed experimental chat saving

These terms describe the accepted creation boundary and proposed mechanism,
not an available capability.

**Runtime file object**:
The conversation-local representation of one intended file, its observed
baseline and proposed content. It coordinates KompoSyn's own access without
claiming exclusive ownership of the external file.
_Avoid_: Filesystem lock, isolated file.

**Save proposal**:
One complete proposed change to an identified target, presented for a user's
once approval against a stated observed baseline.
_Avoid_: Write grant, blanket overwrite permission.

**Save conflict**:
A detected difference between a save proposal's baseline and the target's
current observed state, including an existing creation destination.
_Avoid_: Automatic merge, permission to overwrite.

**Creation scope** (proposed):
A user-selected location in which one-file creation proposals may be prepared
after trusted host eligibility checks. Selecting a creation scope does not
approve any file creation.
_Avoid_: Write grant, working directory, blanket mutation permission.

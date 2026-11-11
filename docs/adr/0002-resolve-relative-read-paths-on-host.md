---
status: accepted
---

# Resolve relative read paths on the host

Resolve model-requested relative file paths against the working directory on
the host before target validation, protected-object checks and exact-file
approval/admission binding. Directory-change commands are distinct from reads;
reading an absolute target or a relative path outside the working directory
does not change that directory. This supports requests such as "read notes.txt"
without requiring the model to know the absolute working-directory path, and
keeps target resolution and authorization with the host rather than the provider.

The original containment clause is superseded by
[ADR-0003](0003-separate-working-directory-from-read-target-eligibility.md).

Implemented by #276 host-side relative-path resolution and #278 exact-target
admission. The resolved absolute path is the target shown for approval and
bound to capture and disclosure. An outside-working-directory path is not
rewritten by navigation.


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

Target eligibility is governed separately by
[ADR-0003](0003-separate-working-directory-from-read-target-eligibility.md).

Implementation is pending; acceptance records the design decision, not shipped
behavior.

See [spec #275](https://github.com/ayeshLK/komposyn/issues/275) and implementation tickets [#276](https://github.com/ayeshLK/komposyn/issues/276), [#278](https://github.com/ayeshLK/komposyn/issues/278).

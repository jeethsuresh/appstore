'use strict';
// A sign-out invalidates memory synchronously, then drains queued disk writes.
// An in-flight refresh may have consumed its old token, but cannot restore an
// account after invalidation or overwrite a newer login.
class CredentialVault {
  constructor({ read, commit, persist }) {
    Object.assign(this, { read, commit, persist });
    this.pending = Promise.resolve();
  }
  replace(expected, next, valid = () => true) {
    const operation = this.pending.catch(() => {}).then(async () => {
      if (this.read() !== expected || !valid()) return false;
      await this.persist(next);
      if (this.read() !== expected || !valid()) {
        await this.persist(this.read());
        return false;
      }
      this.commit(next);
      return true;
    });
    this.pending = operation;
    return operation;
  }
  invalidate() {
    const previous = this.read();
    this.commit(null);
    return { previous, cleared: this.replace(null, null) };
  }
  settled() { return this.pending; }
}
module.exports = { CredentialVault };

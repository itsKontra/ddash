// Test-only: a stand-in for the Web Animations API, which jsdom lacks, so a test can hold Presence mid-exit.

/**
 * Stubs getAnimations (jsdom has none) with one running animation per call,
 * whose finished the test settles: finish() ends the latest, cancel() aborts it.
 */
export function stubAnimations() {
  const calls: { finish: () => void; cancel: () => void }[] = [];
  Element.prototype.getAnimations = function () {
    let finish = () => {};
    let cancel = () => {};
    const finished = new Promise<Animation>((resolve, reject) => {
      finish = () => resolve(this as unknown as Animation);
      cancel = () => reject(new DOMException("aborted", "AbortError"));
    });
    finished.catch(() => {});
    calls.push({ finish, cancel });
    return [{ finished } as Animation];
  };
  return calls;
}

/** Takes the stub away again, so the next test runs with no animations. */
export function unstubAnimations() {
  delete (Element.prototype as Partial<Element>).getAnimations;
}

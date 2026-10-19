/* jsdom has no canvas 2D: every getContext call prints "Not implemented"
   to stderr, once per avatar render, burying real failures in the test log.
   The code under test already reads a null context as "no canvas" (see
   common/avatar blob.ts stillUrl), so answer null quietly. A test that needs
   a canvas stubs getContext itself. */
HTMLCanvasElement.prototype.getContext = (() => null) as typeof HTMLCanvasElement.prototype.getContext;

// Application entry point. All wiring lives in ./bootstrap.js, which registers
// the top-level DOM listeners, starts instrumentation/self-reporting, and kicks
// off start(). Importing it for its side effects preserves the original
// module-load behavior while keeping every source file under the line gate.
import "./bootstrap.js";

import { waitFor } from '@testing-library/react'

// The assignment sections save through a 400 ms debounce. RTL's default 1 s
// wait leaves too little room for that timer in a loaded full-suite run, so a
// wait for the save gets a 3 s budget; it costs nothing when the machine is idle.
export const waitForSave = (assertion: () => void) => waitFor(assertion, { timeout: 3000 })

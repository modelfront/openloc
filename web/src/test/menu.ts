import { screen } from '@testing-library/react'

// A Base UI DropdownMenu renders its items in a portal on later tasks after the
// trigger click, so RTL's default 1 s `findBy` budget loses under full-suite
// load (8 workers). 3 s only costs anything when the machine is slow.
export const menuItem = (testId: string) => screen.findByTestId(testId, undefined, { timeout: 3000 })
export const menuText = (text: string) => screen.findByText(text, undefined, { timeout: 3000 })

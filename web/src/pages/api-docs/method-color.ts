import type { ChipColor } from '@/components/ui/chip'

/** What a method does to data, as a chip colour: reads blue, creates green, changes yellow, deletes red. */
export function methodColor(method: string): ChipColor {
  switch (method) {
    case 'GET':
      return 'blue'
    case 'POST':
      return 'green'
    case 'PUT':
    case 'PATCH':
      return 'yellow'
    case 'DELETE':
      return 'red'
  }
  return 'grey'
}

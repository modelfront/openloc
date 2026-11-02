import { useState, type DragEvent } from 'react'

const hasFiles = (e: DragEvent) => Array.from(e.dataTransfer.types).includes('Files')

/**
 * Files dropped on an element: spread `handlers` on it, and use `dragging`
 * to highlight it while files are over it. Drags of anything but files, and
 * every drag while `disabled`, are left to the browser.
 */
export function useFileDrop(onFiles: (files: File[]) => void, disabled = false) {
  const [dragging, setDragging] = useState(false)
  const over = (e: DragEvent) => {
    if (!hasFiles(e) || disabled) return
    e.preventDefault()
    setDragging(true)
  }
  const handlers = {
    onDragEnter: over,
    onDragOver: over,
    // Moving onto a child also fires dragleave on the element; only leaving
    // the element itself ends the highlight.
    onDragLeave: (e: DragEvent) => {
      if (e.relatedTarget instanceof Node && e.currentTarget.contains(e.relatedTarget)) return
      setDragging(false)
    },
    onDrop: (e: DragEvent) => {
      setDragging(false)
      if (!hasFiles(e) || disabled) return
      e.preventDefault()
      onFiles(Array.from(e.dataTransfer.files))
    },
  }
  return { dragging: dragging && !disabled, handlers }
}

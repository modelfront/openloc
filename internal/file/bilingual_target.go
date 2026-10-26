package file

import (
	"fmt"

	"openloc/internal/langcode"
	"openloc/internal/parsers"
)

// bilingualTarget picks the project target language that receives a bilingual
// file's own translations (XLIFF, PO): the language the file names, matched
// exactly or, failing that, by base language when only one target shares it
// (de-DE → de). A file that names none goes to the project's only target.
// Otherwise no language gets them, and warning says why for the upload
// response. lang is "" when the file has no translations.
func bilingualTarget(in ingest) (lang, warning string) {
	hasTargets := false
	for _, seg := range in.segments {
		if seg.Target != "" {
			hasTargets = true
			break
		}
	}
	if !hasTargets {
		return "", ""
	}
	targets := in.project.TargetLanguages
	fileLang := langcode.Normalize(parsers.FileTargetLanguage(in.format, in.content))
	if fileLang == "" {
		if len(targets) == 1 {
			return targets[0], ""
		}
		return "", "The file's translations were not imported: the file names no target language and the project has several."
	}
	var sameBase []string
	for _, t := range targets {
		if langcode.Normalize(t) == fileLang {
			return t, ""
		}
		if langcode.SameBase(t, fileLang) {
			sameBase = append(sameBase, t)
		}
	}
	if len(sameBase) == 1 {
		return sameBase[0], ""
	}
	return "", fmt.Sprintf("The file's translations were not imported: its target language (%s) is not one of the project's.", fileLang)
}

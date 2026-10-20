-- +goose Up
-- The built-in QE+APE prompt sends the closest TM entry as a PAIR — its own
-- source and target — and the MT prompt drops the key.
--
-- Why: the field used to carry the matched entry's target alone, which on the
-- QE+APE path usually just repeated the translation, because the segment had
-- inherited that very entry. The matched SOURCE is the half the model cannot
-- otherwise see, and the delta between it and the segment's source is exactly
-- where a fuzzy-match defect hides — a TM source reading "Turn off the device"
-- against a segment reading "Do NOT turn off the device" means the inherited
-- translation is wrong in the most dangerous way, and nothing else in the prompt
-- reveals it. The target stays because the pair is what a TM match IS: without
-- it the model cannot tell whether the translation it is judging came from this
-- entry or from somewhere else.
--
-- Nested on purpose: both halves come from one TM hit, and the renderer drops an
-- object whose every member dropped, so a segment with no match carries no
-- "tm_match" key at all rather than an object full of empty strings.
--
-- The MT prompt loses the key because it could never resolve there: the TM phase
-- has already applied every match at or above the same threshold the lookup
-- uses, so any segment reaching MT has none by construction.
--
-- Scoped to rows still holding the exact text the previous migration wrote — an
-- edited default is a decision, not a stale row. `<tm_match>` keeps its original
-- meaning (the matched target), so a custom prompt using it is unaffected;
-- `<tm_match_source>` is purely additive.

UPDATE segment_prompts SET prompt_text = '{
  "source": "<source_text>",
  "translation": "<target_text>",
  "tm_match": {
    "source": "<tm_match_source>",
    "target": "<tm_match>"
  },
  "note": "<note>",
  "comments": "<comments>"
}' WHERE prompt_text = '{
  "source": "<source_text>",
  "translation": "<target_text>",
  "tm_match": "<tm_match>",
  "note": "<note>",
  "comments": "<comments>"
}';

UPDATE segment_prompts SET prompt_text = '{
  "source": "<source_text>"
}' WHERE prompt_text = '{
  "source": "<source_text>",
  "tm_match": "<tm_match>"
}';

-- +goose Down

UPDATE segment_prompts SET prompt_text = '{
  "source": "<source_text>",
  "translation": "<target_text>",
  "tm_match": "<tm_match>",
  "note": "<note>",
  "comments": "<comments>"
}' WHERE prompt_text = '{
  "source": "<source_text>",
  "translation": "<target_text>",
  "tm_match": {
    "source": "<tm_match_source>",
    "target": "<tm_match>"
  },
  "note": "<note>",
  "comments": "<comments>"
}';

UPDATE segment_prompts SET prompt_text = '{
  "source": "<source_text>",
  "tm_match": "<tm_match>"
}' WHERE prompt_text = '{
  "source": "<source_text>"
}';

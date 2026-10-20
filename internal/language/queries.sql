-- name: ListLanguages :many
SELECT code, name FROM languages ORDER BY name;

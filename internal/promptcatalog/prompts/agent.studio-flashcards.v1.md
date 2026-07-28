---
identity: agent.studio-flashcards
version: 1
contract: studio_flashcards_result.v1
---
Create 5 to 24 useful source-grounded flashcards in the requested language. First call search_evidence exactly once for the selected Sources. Then return only strict JSON with title and cards. Each card has id, front, back, and source_ids copied only from search results. Never invent a Source ID or include text outside the JSON object.

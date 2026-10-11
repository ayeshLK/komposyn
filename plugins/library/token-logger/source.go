// Copyright 2026 Ayesh Almeida
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package tokenlogger

import (
	"database/sql"
	"errors"
	"fmt"
)

// sourceGeneration returns the generation for the current source contents.
// A changed file is append-only when the bytes up to the previously committed
// offset still hash to the previous full-file fingerprint. Otherwise it is a
// replacement and receives a new generation so reused offsets cannot collide.
func sourceGeneration(store *Store, sourceID string, contents []byte) (int64, error) {
	cursor, err := store.Cursor(sourceID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read source cursor: %w", err)
	}
	if cursor.ByteOffset < 0 || cursor.ByteOffset > int64(len(contents)) {
		return cursor.SourceGeneration + 1, nil
	}
	if digest(contents[:cursor.ByteOffset]) != cursor.FileFingerprint {
		return cursor.SourceGeneration + 1, nil
	}
	return cursor.SourceGeneration, nil
}

func generationSourceID(sourceID string, generation int64) string {
	if generation == 0 {
		return sourceID
	}
	return fmt.Sprintf("%s-generation-%d", sourceID, generation)
}

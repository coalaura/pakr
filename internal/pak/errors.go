package pak

type errorReader struct {
	err error
}

func (reader *errorReader) Read([]byte) (int, error) {
	return 0, reader.err
}

func (reader *errorReader) Close() error {
	return nil
}

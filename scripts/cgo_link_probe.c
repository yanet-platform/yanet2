// Linked but never run: meson computes its link line for each CGO package,
// and linking it builds the project archives that line names.
int
main(void) {
	return 0;
}

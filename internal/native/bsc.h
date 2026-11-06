#ifndef PAKR_BSC_H
#define PAKR_BSC_H

#include <stddef.h>
#include <libbsc/libbsc.h>

typedef struct pakr_bsc_workspace pakr_bsc_workspace;

int pakr_bsc_init(int features);
pakr_bsc_workspace *pakr_bsc_create(size_t limit);
void pakr_bsc_destroy(pakr_bsc_workspace *workspace);
int pakr_bsc_compress(pakr_bsc_workspace *workspace, const unsigned char *input, unsigned char *output, int size, int hash, int match, int coder, int features);
int pakr_bsc_decompress(pakr_bsc_workspace *workspace, const unsigned char *input, int packed, unsigned char *output, int size, int features);

#endif

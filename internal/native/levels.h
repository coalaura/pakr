#ifndef PAKR_LEVELS_H
#define PAKR_LEVELS_H

/* Backend IDs 1-7 match codec.h; 8=libdeflate, 9=7z, 10=ISA-L. */
int pakr_level(int backend, int level);

#endif

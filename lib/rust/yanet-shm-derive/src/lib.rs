//! The audited derive for the `ShmLayout` trait of `yanet-shm`.
//!
//! The derive is the only way a crate outside `yanet-shm` obtains a
//! `ShmLayout` implementation: the trait is unsafe, so writing it by hand
//! needs `unsafe impl`, which `#![forbid(unsafe_code)]` rejects. The derive
//! accepts only non-generic `#[repr(C)]` structs with named or tuple fields
//! and proves every field is `ShmLayout` itself, so the guarantees of the
//! field types compose into the guarantee of the struct.

use proc_macro::TokenStream;
use proc_macro2::{Span, TokenStream as TokenStream2};
use quote::quote;
use syn::{Data, DeriveInput, Error, Fields, Index, Member, parse_macro_input};

/// Derives `yanet_shm::ShmLayout` for a non-generic `#[repr(C)]` struct.
///
/// The fingerprint folds the size, the alignment and, per field, its offset
/// and fingerprint. The validator checks the struct range and then each
/// field that holds a relative pointer.
#[proc_macro_derive(ShmLayout)]
pub fn derive_shm_layout(input: TokenStream) -> TokenStream {
    let input = parse_macro_input!(input as DeriveInput);
    match expand(&input) {
        Ok(tokens) => tokens.into(),
        Err(err) => err.to_compile_error().into(),
    }
}

fn expand(input: &DeriveInput) -> Result<TokenStream2, Error> {
    check_repr_c(input)?;
    if !input.generics.params.is_empty() {
        return Err(Error::new_spanned(
            &input.generics,
            "ShmLayout cannot be derived for a generic type",
        ));
    }
    let Data::Struct(data) = &input.data else {
        return Err(Error::new(
            Span::call_site(),
            "ShmLayout can only be derived for a struct",
        ));
    };

    let members: Vec<Member> = match &data.fields {
        Fields::Named(fields) => fields
            .named
            .iter()
            .map(|field| Member::Named(field.ident.clone().expect("named field")))
            .collect(),
        Fields::Unnamed(fields) => (0..fields.unnamed.len())
            .map(|idx| Member::Unnamed(Index::from(idx)))
            .collect(),
        Fields::Unit => Vec::new(),
    };
    let types: Vec<_> = data.fields.iter().map(|field| &field.ty).collect();

    let name = &input.ident;
    let shm = quote!(::yanet_shm);

    Ok(quote! {
        const _: () = {
            unsafe impl #shm::ShmLayout for #name {
                const FINGERPRINT: u64 = {
                    let mut hash = #shm::fingerprint::start(
                        ::core::mem::size_of::<#name>(),
                        ::core::mem::align_of::<#name>(),
                    );
                    #(
                        hash = #shm::fingerprint::field(
                            hash,
                            ::core::mem::offset_of!(#name, #members),
                            <#types as #shm::ShmLayout>::FINGERPRINT,
                        );
                    )*
                    hash
                };

                const HAS_REL: bool = false #(|| <#types as #shm::ShmLayout>::HAS_REL)*;

                fn validate(
                    validator: &mut #shm::Validator<'_>,
                    addr: usize,
                ) -> ::core::result::Result<(), #shm::ShmError> {
                    validator.check_range(
                        addr,
                        ::core::mem::size_of::<#name>(),
                        ::core::mem::align_of::<#name>(),
                    )?;
                    if <Self as #shm::ShmLayout>::HAS_REL {
                        #(
                            <#types as #shm::ShmLayout>::validate(
                                validator,
                                addr + ::core::mem::offset_of!(#name, #members),
                            )?;
                        )*
                    }
                    ::core::result::Result::Ok(())
                }
            }
        };
    })
}

/// Accepts `#[repr(C)]`, alone or with `align(N)`; rejects anything else.
fn check_repr_c(input: &DeriveInput) -> Result<(), Error> {
    let mut has_c = false;
    for attr in input.attrs.iter().filter(|attr| attr.path().is_ident("repr")) {
        attr.parse_nested_meta(|meta| {
            if meta.path.is_ident("C") {
                has_c = true;
                Ok(())
            } else if meta.path.is_ident("align") {
                let content;
                syn::parenthesized!(content in meta.input);
                content.parse::<syn::LitInt>()?;
                Ok(())
            } else {
                Err(meta.error("ShmLayout requires #[repr(C)] without packed or other representations"))
            }
        })?;
    }
    if has_c {
        Ok(())
    } else {
        Err(Error::new(Span::call_site(), "ShmLayout requires #[repr(C)]"))
    }
}

//! `#[derive(FfiRecord)]`.

use proc_macro2::TokenStream;
use quote::{quote, quote_spanned};
use syn::{Data, DeriveInput, Fields, spanned::Spanned};

/// Checks the representation: plain `C`. An alignment is refused too, since
/// cbindgen renders `repr(C, align)` as an opaque struct.
fn check_repr(input: &DeriveInput) -> syn::Result<()> {
    let mut repr_c = false;
    for attr in input.attrs.iter().filter(|a| a.path().is_ident("repr")) {
        attr.parse_nested_meta(|meta| {
            if meta.path.is_ident("C") {
                repr_c = true;
                Ok(())
            } else {
                Err(meta.error("FfiRecord supports only repr(C)"))
            }
        })?;
    }
    if repr_c {
        Ok(())
    } else {
        Err(syn::Error::new(input.ident.span(), "FfiRecord requires #[repr(C)]"))
    }
}

pub fn expand(input: &DeriveInput) -> syn::Result<TokenStream> {
    let name = &input.ident;
    if !input.generics.params.is_empty() || input.generics.where_clause.is_some() {
        return Err(syn::Error::new(
            input.generics.span(),
            "FfiRecord cannot be derived for a generic type",
        ));
    }
    let Data::Struct(data) = &input.data else {
        return Err(syn::Error::new(
            name.span(),
            "FfiRecord can only be derived for a struct",
        ));
    };
    let Fields::Named(fields) = &data.fields else {
        return Err(syn::Error::new(name.span(), "FfiRecord requires named fields"));
    };
    if fields.named.is_empty() {
        return Err(syn::Error::new(name.span(), "FfiRecord requires at least one field"));
    }
    check_repr(input)?;
    let p = quote!(::yanet_sdk::__private);

    let drop_msg = format!("FfiRecord: `{name}` needs drop; records are only read");
    let bounds = fields.named.iter().map(|field| {
        let ty = &field.ty;
        quote_spanned!(ty.span()=> __field_is_record::<#ty>();)
    });
    Ok(quote! {
        const _: () = {
            fn __field_is_record<T: #p::FfiRecord>() {}
            fn __fields() {
                #(#bounds)*
            }
            ::core::assert!(!::core::mem::needs_drop::<#name>(), #drop_msg);
        };

        // SAFETY: a non-generic repr(C) struct, not packed, whose every field
        // is a record (checked above) accepts every bit pattern; padding is
        // never a field.
        unsafe impl #p::FfiRecord for #name {}
    })
}
